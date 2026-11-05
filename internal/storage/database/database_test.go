// Internal test package: the up, down, up test needs newProvider and the embedded files, and the
// repeat tests need open with short delays.
package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDatabaseDSN creates a throwaway database, so tests never add tables to the one
// TEST_DATABASE_DSN names and always start with no tables. The DSN must be in URL form: its
// path is replaced.
func newDatabaseDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	target, err := url.Parse(dsn)
	require.NoError(t, err)

	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, admin.Close()) })

	name := fmt.Sprintf("test_%s", strings.ToLower(rand.Text()))
	_, err = admin.ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE %s", name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name))
		assert.NoError(t, err)
	})

	target.Path = name
	return target.String()
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var found sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass($1)::text`, name).Scan(&found))
	return found.Valid
}

func appliedVersions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM goose_db_version WHERE is_applied`).Scan(&n))
	return n
}

func TestOpen_SecondOpenAppliesNothing(t *testing.T) {
	dsn := newDatabaseDSN(t)

	first, err := Open(t.Context(), dsn)
	require.NoError(t, err)
	defer first.Close()
	assert.True(t, tableExists(t, first, "gauges"))
	assert.True(t, tableExists(t, first, "counters"))
	applied := appliedVersions(t, first)

	second, err := Open(t.Context(), dsn)

	require.NoError(t, err)
	defer second.Close()
	assert.True(t, tableExists(t, second, "gauges"))
	assert.True(t, tableExists(t, second, "counters"))
	assert.Equal(t, applied, appliedVersions(t, second))
}

func TestMigrations_UpDownUp_LeavesTables(t *testing.T) {
	db, err := Open(t.Context(), newDatabaseDSN(t))
	require.NoError(t, err)
	defer db.Close()
	provider, err := newProvider(db)
	require.NoError(t, err)

	_, err = provider.Down(t.Context())
	require.NoError(t, err)
	assert.False(t, tableExists(t, db, "gauges"))
	assert.False(t, tableExists(t, db, "counters"))

	results, err := provider.Up(t.Context())
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.True(t, tableExists(t, db, "gauges"))
	assert.True(t, tableExists(t, db, "counters"))
}

func openerGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "database/sql.(*DB).connectionOpener")
}

func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// silentPort accepts connections and never answers, as a stuck database does.
func silentPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, l.Close()) })
	go func() {
		var conns []net.Conn
		for {
			conn, err := l.Accept()
			if err != nil {
				for _, c := range conns {
					c.Close()
				}
				return
			}
			conns = append(conns, conn)
		}
	}()
	return l.Addr().String()
}

func TestOpen_UnusableDatabase_ReturnsErrorInTimeAndLeavesNoPool(t *testing.T) {
	tests := []struct {
		name string
		dsn  func(t *testing.T) string
	}{
		{name: "closed port", dsn: func(t *testing.T) string {
			return fmt.Sprintf("postgres://u:secretpw@%s/x?sslmode=disable", closedPort(t))
		}},
		{name: "server that never answers", dsn: func(t *testing.T) string {
			return fmt.Sprintf("postgres://u:secretpw@%s/x?sslmode=disable", silentPort(t))
		}},
		{name: "malformed URL", dsn: func(*testing.T) string { return "postgres://u:secretpw@localhost:notaport/x" }},
		{name: "malformed key-value", dsn: func(*testing.T) string { return "host=localhost password=secretpw port=notaport" }},
		{name: "spaces around the password's equals sign", dsn: func(*testing.T) string { return "password = secretpw port=notaport" }},
		{name: "password with no key", dsn: func(*testing.T) string { return "localhost:5432 secretpw" }},
		{name: "not a DSN at all", dsn: func(*testing.T) string { return "not a dsn secretpw" }},
	}
	const timeout = 300 * time.Millisecond
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn := tt.dsn(t)
			before := openerGoroutines()
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			errs := make(chan error, 1)
			var db *sql.DB

			go func() {
				var err error
				db, err = Open(ctx, dsn)
				errs <- err
			}()

			select {
			case err := <-errs:
				require.Error(t, err)
				assert.Nil(t, db)
				assert.NotContains(t, err.Error(), "secretpw")
			case <-time.After(timeout + 2*time.Second):
				t.Fatal("Open outlived its context")
			}
			// Close stops the opener goroutine, and a pool left open would keep it forever.
			assert.Eventually(t, func() bool { return openerGoroutines() <= before }, time.Second, 10*time.Millisecond)
		})
	}
}

// proxy forwards a port to the real database once opened. Until then nothing listens there, so the
// driver sees a real refused connection without the container being stopped.
type proxy struct {
	t           *testing.T
	addr        string
	target      string
	connections atomic.Int32

	mu       sync.Mutex
	closed   bool
	listener net.Listener
	conns    []net.Conn
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	p := &proxy{t: t, addr: closedPort(t), target: target}
	t.Cleanup(p.close)
	return p
}

func (p *proxy) open() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	l, err := net.Listen("tcp", p.addr)
	if !assert.NoError(p.t, err) {
		return
	}
	p.listener = l
	go p.serve(l)
}

func (p *proxy) openAfter(d time.Duration) {
	timer := time.AfterFunc(d, p.open)
	p.t.Cleanup(func() { timer.Stop() })
}

func (p *proxy) serve(l net.Listener) {
	for {
		client, err := l.Accept()
		if err != nil {
			return
		}
		p.connections.Add(1)
		server, err := net.Dial("tcp", p.target)
		if !assert.NoError(p.t, err) {
			client.Close()
			return
		}
		p.mu.Lock()
		p.conns = append(p.conns, client, server)
		p.mu.Unlock()
		go forward(client, server)
		go forward(server, client)
	}
}

func forward(to, from net.Conn) {
	io.Copy(to, from)
	to.Close()
	from.Close()
}

func (p *proxy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.listener != nil {
		p.listener.Close()
	}
	for _, c := range p.conns {
		c.Close()
	}
}

// through returns dsn pointed at the proxy. TLS stays off, so one attempt is one connection.
func (p *proxy) through(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Host = p.addr
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func hostOf(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	return u.Host
}

func TestOpen_DatabaseComingUpDuringAPause_Succeeds(t *testing.T) {
	const opensAfter, pause = 200 * time.Millisecond, 600 * time.Millisecond
	dsn := newDatabaseDSN(t)
	p := newProxy(t, hostOf(t, dsn))
	p.openAfter(opensAfter)
	start := time.Now()

	db, err := open(t.Context(), p.through(t, dsn), []time.Duration{pause})

	require.NoError(t, err)
	defer db.Close()
	assert.GreaterOrEqual(t, time.Since(start), pause, "the first attempt must have been refused")
	assert.True(t, tableExists(t, db, "gauges"))
}

func TestOpen_ClosedPort_ErrorsAfterEveryAttempt(t *testing.T) {
	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	start := time.Now()

	db, err := open(t.Context(), fmt.Sprintf("postgres://u:p@%s/x?sslmode=disable", closedPort(t)), delays)

	assert.Nil(t, db)
	var connectErr *pgconn.ConnectError
	assert.ErrorAs(t, err, &connectErr)
	assert.ErrorContains(t, err, "after 4 attempts")
	assert.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond)
}

func TestOpen_ClosedPort_StopsAtTheContextBound(t *testing.T) {
	const timeout = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	start := time.Now()

	db, err := Open(ctx, fmt.Sprintf("postgres://u:p@%s/x?sslmode=disable", closedPort(t)))

	assert.Nil(t, db)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	var connectErr *pgconn.ConnectError
	assert.ErrorAs(t, err, &connectErr)
	assert.Less(t, time.Since(start), retryPauseBelow)
}

// Below the first default pause, so a pause that ignored the context would fail the check.
const retryPauseBelow = 900 * time.Millisecond

func TestOpen_WrongPassword_FailsWithoutRepeats(t *testing.T) {
	dsn := newDatabaseDSN(t)
	p := newProxy(t, hostOf(t, dsn))
	p.open()
	u, err := url.Parse(p.through(t, dsn))
	require.NoError(t, err)
	u.User = url.UserPassword(u.User.Username(), "wrong")

	db, err := open(t.Context(), u.String(), []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})

	assert.Nil(t, db)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, pgerrcode.InvalidPassword, pgErr.Code)
	assert.EqualValues(t, 1, p.connections.Load())
}

// plainDatabase opens a throwaway database with no migrations, for failures any table can produce.
func plainDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", newDatabaseDSN(t))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	return db
}

func pingError(t *testing.T, dsn string, timeout time.Duration) error {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	return db.PingContext(ctx)
}

func withDatabaseURL(t *testing.T, change func(u *url.URL)) string {
	t.Helper()
	u, err := url.Parse(newDatabaseDSN(t))
	require.NoError(t, err)
	change(u)
	return u.String()
}

func uniqueViolation(t *testing.T) error {
	t.Helper()
	db := plainDatabase(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE t (id int PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO t VALUES (1)`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO t VALUES (1)`)
	return err
}

// terminatedBackend cuts a connection running a statement, as a shutting down server does. It
// returns the statement's error and the error of the next call on that connection.
func terminatedBackend(t *testing.T) (during, next error) {
	t.Helper()
	db := plainDatabase(t)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer conn.Close()
	var pid int
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid))

	errs := make(chan error, 1)
	go func() {
		_, err := conn.ExecContext(t.Context(), `SELECT pg_sleep(30)`)
		errs <- err
	}()
	require.Eventually(t, func() bool {
		var state string
		err := db.QueryRowContext(t.Context(), `SELECT state FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&state)
		return err == nil && state == "active"
	}, 5*time.Second, 10*time.Millisecond)
	_, err = db.ExecContext(t.Context(), `SELECT pg_terminate_backend($1)`, pid)
	require.NoError(t, err)

	during = <-errs
	_, next = conn.ExecContext(t.Context(), `SELECT 1`)
	return during, next
}

func deadlock(t *testing.T) error {
	t.Helper()
	db := plainDatabase(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE t (id int PRIMARY KEY); INSERT INTO t VALUES (1), (2)`)
	require.NoError(t, err)
	first, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer first.Rollback()
	second, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer second.Rollback()
	_, err = first.ExecContext(t.Context(), `UPDATE t SET id = 1 WHERE id = 1`)
	require.NoError(t, err)
	_, err = second.ExecContext(t.Context(), `UPDATE t SET id = 2 WHERE id = 2`)
	require.NoError(t, err)

	errs := make(chan error, 2)
	go func() {
		_, err := first.ExecContext(t.Context(), `UPDATE t SET id = 2 WHERE id = 2`)
		errs <- err
	}()
	go func() {
		_, err := second.ExecContext(t.Context(), `UPDATE t SET id = 1 WHERE id = 1`)
		errs <- err
	}()
	return errors.Join(<-errs, <-errs)
}

func TestIsRetriable_RealFailures_OnlyAnUnsentStatementRepeats(t *testing.T) {
	tests := []struct {
		name           string
		err            func(t *testing.T) error
		wantCode       string
		wantConnectErr bool
		wantRetriable  bool
		wantIsErr      error
	}{
		{
			name: "refused connection",
			err: func(t *testing.T) error {
				return pingError(t, fmt.Sprintf("postgres://u:p@%s/x?sslmode=disable", closedPort(t)), time.Second)
			},
			wantConnectErr: true,
			wantRetriable:  true,
		},
		{
			name: "connect that times out sent no statement",
			err: func(t *testing.T) error {
				return pingError(t, fmt.Sprintf("postgres://u:p@%s/x?sslmode=disable", silentPort(t)), 100*time.Millisecond)
			},
			wantConnectErr: true,
			wantRetriable:  true,
			wantIsErr:      context.DeadlineExceeded,
		},
		{
			name: "wrong password",
			err: func(t *testing.T) error {
				return pingError(t, withDatabaseURL(t, func(u *url.URL) {
					u.User = url.UserPassword(u.User.Username(), "wrong")
				}), 5*time.Second)
			},
			wantCode:       pgerrcode.InvalidPassword,
			wantConnectErr: true,
		},
		{
			name: "missing database",
			err: func(t *testing.T) error {
				return pingError(t, withDatabaseURL(t, func(u *url.URL) { u.Path = "no_such_database" }), 5*time.Second)
			},
			wantCode:       pgerrcode.InvalidCatalogName,
			wantConnectErr: true,
		},
		{name: "unique violation", err: uniqueViolation, wantCode: pgerrcode.UniqueViolation},
		{
			name: "undefined table",
			err: func(t *testing.T) error {
				_, err := plainDatabase(t).ExecContext(t.Context(), `SELECT * FROM no_such_table`)
				return err
			},
			wantCode: pgerrcode.UndefinedTable,
		},
		{
			name: "context cancelled before the statement",
			err: func(t *testing.T) error {
				db := plainDatabase(t)
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err := db.ExecContext(ctx, `SELECT 1`)
				return err
			},
			wantIsErr: context.Canceled,
		},
		{
			name: "context deadline during the statement",
			err: func(t *testing.T) error {
				db := plainDatabase(t)
				require.NoError(t, db.PingContext(t.Context()))
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				_, err := db.ExecContext(ctx, `SELECT pg_sleep(30)`)
				return err
			},
			wantIsErr: context.DeadlineExceeded,
		},
		{
			name: "backend terminated during the statement",
			err: func(t *testing.T) error {
				during, _ := terminatedBackend(t)
				return during
			},
			wantCode: pgerrcode.AdminShutdown,
		},
		{
			name: "next call on the cut connection",
			err: func(t *testing.T) error {
				_, next := terminatedBackend(t)
				return next
			},
			wantIsErr: driver.ErrBadConn,
		},
		{name: "deadlock", err: deadlock, wantCode: pgerrcode.DeadlockDetected},
		{name: "plain error", err: func(*testing.T) error { return errors.New("connection refused") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.err(t)
			require.Error(t, err)

			var pgErr *pgconn.PgError
			if tt.wantCode != "" {
				require.ErrorAs(t, err, &pgErr)
				assert.Equal(t, tt.wantCode, pgErr.Code)
			} else {
				assert.False(t, errors.As(err, &pgErr), "no SQLSTATE expected in %v", err)
			}
			var connectErr *pgconn.ConnectError
			assert.Equal(t, tt.wantConnectErr, errors.As(err, &connectErr), "%v", err)
			if tt.wantIsErr != nil {
				assert.ErrorIs(t, err, tt.wantIsErr)
			}

			assert.Equal(t, tt.wantRetriable, IsRetriable(err), "%v", err)
			assert.Equal(t, tt.wantRetriable, IsRetriable(fmt.Errorf("set gauge %q: %w", "x", err)), "through %%w")
		})
	}
}

// Hand-built on purpose, and only here: none of the connection failures measured on a real Postgres
// returned class 08, and a real 57P03 needs a restart of the container the other tests use.
func TestIsRetriable_ServerCodes_OnlyConnectionCodesRepeat(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "connection exception", code: pgerrcode.ConnectionException, want: true},
		{name: "connection does not exist", code: pgerrcode.ConnectionDoesNotExist, want: true},
		{name: "connection failure", code: pgerrcode.ConnectionFailure, want: true},
		{name: "client unable to establish", code: pgerrcode.SQLClientUnableToEstablishSQLConnection, want: true},
		{name: "server rejected establishment", code: pgerrcode.SQLServerRejectedEstablishmentOfSQLConnection, want: true},
		{name: "transaction resolution unknown leaves the commit's result open", code: pgerrcode.TransactionResolutionUnknown, want: false},
		{name: "protocol violation", code: pgerrcode.ProtocolViolation, want: true},
		{name: "cannot connect now", code: pgerrcode.CannotConnectNow, want: true},
		{name: "crash shutdown is a neighbour of 57P03", code: pgerrcode.CrashShutdown, want: false},
		{name: "too many connections is a neighbour by meaning", code: pgerrcode.TooManyConnections, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &pgconn.PgError{Code: tt.code}

			assert.Equal(t, tt.want, IsRetriable(err))
			assert.Equal(t, tt.want, IsRetriable(fmt.Errorf("add counter %q: %w", "x", err)), "through %%w")
		})
	}
}

func TestIsConnectionLost_RealFailures_OnlyALostConnectionCounts(t *testing.T) {
	tests := []struct {
		name string
		err  func(t *testing.T) error
		want bool
	}{
		{
			name: "backend terminated during the statement",
			err: func(t *testing.T) error {
				during, _ := terminatedBackend(t)
				var pgErr *pgconn.PgError
				require.ErrorAs(t, during, &pgErr)
				require.Equal(t, pgerrcode.AdminShutdown, pgErr.Code)
				return during
			},
			want: true,
		},
		{
			name: "next call on the cut connection",
			err: func(t *testing.T) error {
				_, next := terminatedBackend(t)
				require.ErrorIs(t, next, driver.ErrBadConn)
				return next
			},
			want: true,
		},
		{
			name: "refused connection",
			err: func(t *testing.T) error {
				err := pingError(t, fmt.Sprintf("postgres://u:p@%s/x?sslmode=disable", closedPort(t)), time.Second)
				var connectErr *pgconn.ConnectError
				require.ErrorAs(t, err, &connectErr)
				return err
			},
		},
		{name: "unique violation", err: uniqueViolation},
		{
			name: "context cancelled before the statement",
			err: func(t *testing.T) error {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err := plainDatabase(t).ExecContext(ctx, `SELECT 1`)
				require.ErrorIs(t, err, context.Canceled)
				return err
			},
		},
		{
			name: "context deadline during the statement",
			err: func(t *testing.T) error {
				db := plainDatabase(t)
				require.NoError(t, db.PingContext(t.Context()))
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				_, err := db.ExecContext(ctx, `SELECT pg_sleep(30)`)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				return err
			},
		},
		{name: "crash shutdown is a neighbour of 57P01", err: func(*testing.T) error {
			return &pgconn.PgError{Code: pgerrcode.CrashShutdown}
		}},
		{name: "cannot connect now is a neighbour of 57P01", err: func(*testing.T) error {
			return &pgconn.PgError{Code: pgerrcode.CannotConnectNow}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.err(t)
			require.Error(t, err)

			assert.Equal(t, tt.want, IsConnectionLost(err), "%v", err)
			assert.Equal(t, tt.want, IsConnectionLost(fmt.Errorf("read gauge %q: %w", "x", err)), "through %%w")
		})
	}
}
