package pgstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/pgstore"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	p := &proxy{t: t, addr: addr, target: target}
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

// behindProxy is one throwaway database with two ways in: direct prepares and reads back
// past the outage, and store goes through the proxy.
type behindProxy struct {
	direct *pgstore.Storage
	db     *sql.DB
	store  *pgstore.Storage
	proxy  *proxy
}

// newBehindProxy opens no idle connections, so at the proxy one attempt is one connection.
func newBehindProxy(t *testing.T) *behindProxy {
	t.Helper()
	dsn := newDatabaseDSN(t)
	direct := pgstore.New(newDatabaseAt(t, dsn))

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	p := newProxy(t, u.Host)
	u.Host = p.addr
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()

	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	db.SetMaxIdleConns(0)

	return &behindProxy{direct: direct, db: db, store: pgstore.New(db), proxy: p}
}

// The first attempt has opensAfter to be refused, far more than a refusal on loopback takes. The
// elapsed time then proves it was refused: an attempt that got through would end before pause.
const (
	opensAfter = 200 * time.Millisecond
	pause      = 600 * time.Millisecond
)

func TestStorage_Outage_EveryMethodSucceedsOnceThePortOpens(t *testing.T) {
	for _, tt := range everyMethod {
		t.Run(tt.name, func(t *testing.T) {
			b := newBehindProxy(t)
			require.NoError(t, b.direct.SetGauge(t.Context(), "x", 1))
			require.NoError(t, b.direct.AddCounter(t.Context(), "x", 1))
			b.store.SetDelays([]time.Duration{pause})
			b.proxy.openAfter(opensAfter)
			start := time.Now()

			err := tt.call(t.Context(), b.store)

			require.NoError(t, err)
			assert.GreaterOrEqual(t, time.Since(start), pause)
			assert.Zero(t, b.db.Stats().InUse)
		})
	}
}

func TestStorage_Outage_ReturnsWhatWasStored(t *testing.T) {
	b := newBehindProxy(t)
	require.NoError(t, b.direct.SetGauge(t.Context(), "temp", 36.6))
	require.NoError(t, b.direct.AddCounter(t.Context(), "hits", 527))
	b.store.SetDelays([]time.Duration{pause})
	b.proxy.openAfter(opensAfter)

	snap, err := b.store.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": 36.6},
		Counters: map[string]metrics.Counter{"hits": 527},
	}, snap)

	gauge, err := b.store.Gauge(t.Context(), "temp")
	require.NoError(t, err)
	assert.Equal(t, metrics.Gauge(36.6), gauge)
	counter, err := b.store.Counter(t.Context(), "hits")
	require.NoError(t, err)
	assert.Equal(t, metrics.Counter(527), counter)
}

func TestStorage_Outage_CounterIsAddedOnce(t *testing.T) {
	tests := []struct {
		name string
		call func(ctx context.Context, s *pgstore.Storage) error
	}{
		{name: "AddCounter", call: func(ctx context.Context, s *pgstore.Storage) error {
			return s.AddCounter(ctx, "hits", 5)
		}},
		{name: "UpdateBatch", call: func(ctx context.Context, s *pgstore.Storage) error {
			return s.UpdateBatch(ctx, metrics.Batch{Counters: map[string]metrics.Counter{"hits": 5}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBehindProxy(t)
			require.NoError(t, b.direct.AddCounter(t.Context(), "hits", 10))
			b.store.SetDelays([]time.Duration{pause})
			b.proxy.openAfter(opensAfter)
			start := time.Now()

			require.NoError(t, tt.call(t.Context(), b.store))

			assert.GreaterOrEqual(t, time.Since(start), pause)
			got, err := b.direct.Counter(t.Context(), "hits")
			require.NoError(t, err)
			assert.Equal(t, metrics.Counter(15), got)
		})
	}
}

// The pause outlasts queryTimeout, so a timeout shared by the attempts would end the repeat.
func TestStorage_Outage_PauseLongerThanTheQueryTimeoutKeepsRepeating(t *testing.T) {
	const longPause = 3200 * time.Millisecond
	b := newBehindProxy(t)
	b.store.SetDelays([]time.Duration{longPause})
	b.proxy.openAfter(opensAfter)

	err := b.store.SetGauge(t.Context(), "x", 1)

	require.NoError(t, err)
	got, err := b.direct.Gauge(t.Context(), "x")
	require.NoError(t, err)
	assert.Equal(t, metrics.Gauge(1), got)
}

func TestStorage_New_RepeatsWithTheDefaultDelays(t *testing.T) {
	b := newBehindProxy(t)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := b.store.SetGauge(ctx, "x", 1)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "stopped after attempt 1")
}

func TestStorage_ClosedPort_ErrorsAfterEveryAttempt(t *testing.T) {
	delays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	for _, tt := range everyMethod {
		t.Run(tt.name, func(t *testing.T) {
			b := newBehindProxy(t)
			b.store.SetDelays(delays)
			start := time.Now()

			err := tt.call(t.Context(), b.store)

			var connectErr *pgconn.ConnectError
			assert.ErrorAs(t, err, &connectErr)
			assert.ErrorContains(t, err, "after 4 attempts")
			assert.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond)
		})
	}
}

func TestStorage_NonRetriableError_MakesOneAttempt(t *testing.T) {
	overflowing := metrics.Batch{Counters: map[string]metrics.Counter{"hits": 1}}
	tests := []struct {
		name string
		call func(ctx context.Context, s *pgstore.Storage) error
		text string
	}{
		{name: "AddCounter overflow", text: "bigint out of range", call: func(ctx context.Context, s *pgstore.Storage) error {
			return s.AddCounter(ctx, "hits", 1)
		}},
		{name: "UpdateBatch overflow", text: "bigint out of range", call: func(ctx context.Context, s *pgstore.Storage) error {
			return s.UpdateBatch(ctx, overflowing)
		}},
		{name: "Gauge not found", text: metrics.ErrNotFound.Error(), call: func(ctx context.Context, s *pgstore.Storage) error {
			_, err := s.Gauge(ctx, "nope")
			return err
		}},
		{name: "Counter not found", text: metrics.ErrNotFound.Error(), call: func(ctx context.Context, s *pgstore.Storage) error {
			_, err := s.Counter(ctx, "nope")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBehindProxy(t)
			require.NoError(t, b.direct.AddCounter(t.Context(), "hits", math.MaxInt64))
			b.store.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
			b.proxy.open()

			err := tt.call(t.Context(), b.store)

			require.ErrorContains(t, err, tt.text)
			assert.NotContains(t, err.Error(), "attempt")
			assert.EqualValues(t, 1, b.proxy.connections.Load())
		})
	}
}

func TestStorage_Read_NotFoundStaysTheSameError(t *testing.T) {
	b := newBehindProxy(t)
	b.proxy.open()

	_, gaugeErr := b.store.Gauge(t.Context(), "nope")
	_, counterErr := b.store.Counter(t.Context(), "nope")

	assert.True(t, gaugeErr == metrics.ErrNotFound)
	assert.True(t, counterErr == metrics.ErrNotFound)
}

func TestStorage_CallerCancel_StopsThePause(t *testing.T) {
	for _, tt := range everyMethod {
		t.Run(tt.name, func(t *testing.T) {
			b := newBehindProxy(t)
			const longPause = 5 * time.Second
			b.store.SetDelays([]time.Duration{longPause})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			time.AfterFunc(50*time.Millisecond, cancel)
			start := time.Now()

			err := tt.call(ctx, b.store)

			assert.ErrorIs(t, err, context.Canceled)
			var connectErr *pgconn.ConnectError
			assert.ErrorAs(t, err, &connectErr)
			assert.Less(t, time.Since(start), longPause)
		})
	}
}

// silentListener accepts connections, counts them and never answers, as a stuck database does.
func silentListener(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var accepted atomic.Int32
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		assert.NoError(t, l.Close())
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return l.Addr().String(), &accepted
}

// A connect cut by the deadline is retriable, since nothing was sent, so only the context stands
// between it and a repeat.
func TestStorage_ParentDeadlineDuringConnect_DoesNotRepeat(t *testing.T) {
	addr, accepted := silentListener(t)
	db, err := sql.Open("pgx", fmt.Sprintf("postgres://u:p@%s/x?sslmode=disable", addr))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()

	err = pgstore.New(db).SetGauge(ctx, "x", 1)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	var connectErr *pgconn.ConnectError
	assert.ErrorAs(t, err, &connectErr)
	assert.Less(t, time.Since(start), 900*time.Millisecond, "below the first default pause")
	assert.EqualValues(t, 1, accepted.Load())
}

// raiseOnCounterWrite makes the real server answer class 08 from inside a transaction, after the
// gauges of the batch are written. A sequence counts the attempts: nextval survives a rollback.
func raiseOnCounterWrite(t *testing.T, db *sql.DB, failures int) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(`
		CREATE SEQUENCE attempts;
		CREATE FUNCTION raise_class_08() RETURNS trigger AS $$
		BEGIN
			IF nextval('attempts') <= %d THEN
				RAISE EXCEPTION 'cut' USING ERRCODE = '%s';
			END IF;
			RETURN NEW;
		END
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER raise_class_08 BEFORE INSERT ON counters
			FOR EACH ROW EXECUTE FUNCTION raise_class_08();`, failures, pgerrcode.ConnectionFailure))
	require.NoError(t, err)
}

func TestStorage_UpdateBatch_Class08RepeatsTheWholeTransaction(t *testing.T) {
	tests := []struct {
		name         string
		failures     int
		wantAttempts int
		wantErr      bool
		wantGauge    metrics.Gauge
		wantCounter  metrics.Counter
	}{
		{name: "second attempt lands once", failures: 1, wantAttempts: 2, wantGauge: 1, wantCounter: 15},
		{name: "last attempt lands once", failures: 3, wantAttempts: 4, wantGauge: 1, wantCounter: 15},
		{name: "every attempt fails and nothing stays", failures: 100, wantAttempts: 4, wantErr: true, wantGauge: 36.6, wantCounter: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newDatabase(t)
			db.SetMaxOpenConns(1)
			s := pgstore.New(db)
			s.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
			require.NoError(t, s.SetGauge(t.Context(), "temp", 36.6))
			require.NoError(t, s.AddCounter(t.Context(), "hits", 10))
			raiseOnCounterWrite(t, db, tt.failures)
			backendPID := func() int {
				var pid int
				require.NoError(t, db.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid))
				return pid
			}
			before := backendPID()

			err := s.UpdateBatch(t.Context(), metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 1},
				Counters: map[string]metrics.Counter{"hits": 5},
			})

			if tt.wantErr {
				var pgErr *pgconn.PgError
				require.ErrorAs(t, err, &pgErr)
				assert.Equal(t, pgerrcode.ConnectionFailure, pgErr.Code)
				assert.ErrorContains(t, err, "batch counters")
			} else {
				require.NoError(t, err)
			}
			assert.Zero(t, db.Stats().InUse)
			assert.Equal(t, before, backendPID(), "a repeat must not cost the connection")

			var attempts int
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT last_value FROM attempts`).Scan(&attempts))
			assert.Equal(t, tt.wantAttempts, attempts)
			snap, err := s.Snapshot(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.wantGauge, snap.Gauges["temp"])
			assert.Equal(t, tt.wantCounter, snap.Counters["hits"])
		})
	}
}

type sqlKey struct{}

type statement struct {
	sql string
	err error
}

// statementLog records what the driver really ran and what each statement returned. The ping pgx sends
// on a connection idle over a second is not a statement, so a call that went that way leaves no failure
// here: this is what tells a real 57P01 from a pass through the ping.
type statementLog struct {
	mu   sync.Mutex
	seen []statement
}

func (l *statementLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, sqlKey{}, data.SQL)
}

func (l *statementLog) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	l.mu.Lock()
	defer l.mu.Unlock()
	query, _ := ctx.Value(sqlKey{}).(string)
	l.seen = append(l.seen, statement{sql: query, err: data.Err})
}

func (l *statementLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = nil
}

func (l *statementLog) statements() []statement {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.seen)
}

func (l *statementLog) begins() int {
	count := 0
	for _, st := range l.statements() {
		if strings.HasPrefix(strings.ToLower(st.sql), "begin") {
			count++
		}
	}
	return count
}

// traced is one throwaway database with two ways in: direct prepares, reads back and terminates, and
// store runs on a pool of one connection, so the next call takes the connection direct terminated.
type traced struct {
	direct *sql.DB
	db     *sql.DB
	store  *pgstore.Storage
	log    *statementLog
}

func newTraced(t *testing.T) *traced {
	t.Helper()
	dsn := newDatabaseDSN(t)
	direct := newDatabaseAt(t, dsn)

	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	log := &statementLog{}
	config.Tracer = log
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)

	store := pgstore.New(db)
	store.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	return &traced{direct: direct, db: db, store: store, log: log}
}

// cutIdleConnection leaves the pool holding a connection whose backend is gone and whose session pgx reset
// under a second ago, so pgx skips its ping and the next statement meets the FATAL left in the socket. A
// connection on its first reuse would not do: pgx always pings it, and the statement goes to a new one.
func (tr *traced) cutIdleConnection(t *testing.T) {
	t.Helper()
	var pid int
	require.NoError(t, tr.db.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&pid))
	_, err := tr.direct.ExecContext(t.Context(), `SELECT pg_terminate_backend($1)`, pid)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var alive int
		err := tr.direct.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&alive)
		return err == nil && alive == 0
	}, 500*time.Millisecond, time.Millisecond)
	tr.log.reset()
}

func assertAdminShutdown(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if assert.ErrorAs(t, err, &pgErr) {
		assert.Equal(t, pgerrcode.AdminShutdown, pgErr.Code)
	}
}

func TestStorage_StaleConnection_RepeatsWhereACounterCannotDouble(t *testing.T) {
	tests := []struct {
		name        string
		call        func(t *testing.T, s *pgstore.Storage) error
		wantGauge   metrics.Gauge
		wantCounter metrics.Counter
	}{
		{
			name: "SetGauge",
			call: func(t *testing.T, s *pgstore.Storage) error {
				return s.SetGauge(t.Context(), "temp", 1)
			},
			wantGauge:   1,
			wantCounter: 10,
		},
		{
			name: "Gauge",
			call: func(t *testing.T, s *pgstore.Storage) error {
				got, err := s.Gauge(t.Context(), "temp")
				assert.Equal(t, metrics.Gauge(36.6), got)
				return err
			},
			wantGauge:   36.6,
			wantCounter: 10,
		},
		{
			name: "Counter",
			call: func(t *testing.T, s *pgstore.Storage) error {
				got, err := s.Counter(t.Context(), "hits")
				assert.Equal(t, metrics.Counter(10), got)
				return err
			},
			wantGauge:   36.6,
			wantCounter: 10,
		},
		{
			name: "Snapshot",
			call: func(t *testing.T, s *pgstore.Storage) error {
				got, err := s.Snapshot(t.Context())
				assert.Equal(t, metrics.Snapshot{
					Gauges:   map[string]metrics.Gauge{"temp": 36.6},
					Counters: map[string]metrics.Counter{"hits": 10},
				}, got)
				return err
			},
			wantGauge:   36.6,
			wantCounter: 10,
		},
		{
			name: "UpdateBatch",
			call: func(t *testing.T, s *pgstore.Storage) error {
				return s.UpdateBatch(t.Context(), metrics.Batch{
					Gauges:   map[string]metrics.Gauge{"temp": 1},
					Counters: map[string]metrics.Counter{"hits": 5},
				})
			},
			wantGauge:   1,
			wantCounter: 15,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTraced(t)
			require.NoError(t, tr.store.SetGauge(t.Context(), "temp", 36.6))
			require.NoError(t, tr.store.AddCounter(t.Context(), "hits", 10))
			tr.cutIdleConnection(t)

			err := tt.call(t, tr.store)

			require.NoError(t, err)
			statements := tr.log.statements()
			require.Greater(t, len(statements), 1)
			assertAdminShutdown(t, statements[0].err)
			for _, st := range statements[1:] {
				assert.NoError(t, st.err, st.sql)
			}
			var gauge float64
			require.NoError(t, tr.direct.QueryRowContext(t.Context(), `SELECT value FROM gauges WHERE name = 'temp'`).Scan(&gauge))
			assert.Equal(t, float64(tt.wantGauge), gauge)
			var counter int64
			require.NoError(t, tr.direct.QueryRowContext(t.Context(), `SELECT delta FROM counters WHERE name = 'hits'`).Scan(&counter))
			assert.Equal(t, int64(tt.wantCounter), counter)
		})
	}
}

func TestStorage_AddCounter_StaleConnectionIsNotRepeated(t *testing.T) {
	tr := newTraced(t)
	require.NoError(t, tr.store.AddCounter(t.Context(), "hits", 10))
	tr.cutIdleConnection(t)

	err := tr.store.AddCounter(t.Context(), "hits", 5)

	require.Error(t, err)
	assertAdminShutdown(t, err)
	assert.NotContains(t, err.Error(), "attempt")
	statements := tr.log.statements()
	require.Len(t, statements, 1)
	assertAdminShutdown(t, statements[0].err)
	var counter int64
	require.NoError(t, tr.direct.QueryRowContext(t.Context(), `SELECT delta FROM counters WHERE name = 'hits'`).Scan(&counter))
	assert.EqualValues(t, 10, counter)
}

// The backend terminates itself from a trigger, so the connection goes at a chosen statement after BeginTx.
// The commit case aborts that commit, so it shows one attempt, not a commit that landed and lost its answer.
func TestStorage_UpdateBatch_ConnectionLostAfterBegin_MakesOneAttempt(t *testing.T) {
	tests := []struct {
		name     string
		trigger  string
		wantText string
	}{
		{
			name:     "at the gauges",
			trigger:  `CREATE TRIGGER terminate_self BEFORE INSERT ON gauges FOR EACH ROW EXECUTE FUNCTION terminate_self()`,
			wantText: "batch gauges",
		},
		{
			name:     "at the counters",
			trigger:  `CREATE TRIGGER terminate_self BEFORE INSERT ON counters FOR EACH ROW EXECUTE FUNCTION terminate_self()`,
			wantText: "batch counters",
		},
		{
			name: "at the commit",
			trigger: `CREATE CONSTRAINT TRIGGER terminate_self AFTER INSERT OR UPDATE ON counters
				DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION terminate_self()`,
			wantText: "commit batch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTraced(t)
			require.NoError(t, tr.store.SetGauge(t.Context(), "temp", 36.6))
			require.NoError(t, tr.store.AddCounter(t.Context(), "hits", 10))
			_, err := tr.direct.ExecContext(t.Context(), `
				CREATE FUNCTION terminate_self() RETURNS trigger AS $$
				BEGIN
					PERFORM pg_terminate_backend(pg_backend_pid());
					RETURN NEW;
				END
				$$ LANGUAGE plpgsql;`)
			require.NoError(t, err)
			_, err = tr.direct.ExecContext(t.Context(), tt.trigger)
			require.NoError(t, err)
			tr.log.reset()

			err = tr.store.UpdateBatch(t.Context(), metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 1},
				Counters: map[string]metrics.Counter{"hits": 5},
			})

			require.Error(t, err)
			assertAdminShutdown(t, err)
			assert.ErrorContains(t, err, tt.wantText)
			assert.NotContains(t, err.Error(), "attempt")
			assert.Equal(t, 1, tr.log.begins())
			var gauge float64
			require.NoError(t, tr.direct.QueryRowContext(t.Context(), `SELECT value FROM gauges WHERE name = 'temp'`).Scan(&gauge))
			assert.Equal(t, 36.6, gauge)
			var counter int64
			require.NoError(t, tr.direct.QueryRowContext(t.Context(), `SELECT delta FROM counters WHERE name = 'hits'`).Scan(&counter))
			assert.EqualValues(t, 10, counter)
		})
	}
}

func TestStorage_UpdateBatch_BeginFailureThatIsNotALostConnection_MakesOneAttempt(t *testing.T) {
	u, err := url.Parse(newDatabaseDSN(t))
	require.NoError(t, err)
	p := newProxy(t, u.Host)
	p.open()
	u.Host = p.addr
	u.User = url.UserPassword(u.User.Username(), "wrong")
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	s := pgstore.New(db)
	s.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})

	err = s.UpdateBatch(t.Context(), metrics.Batch{Gauges: map[string]metrics.Gauge{"temp": 1}})

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, pgerrcode.InvalidPassword, pgErr.Code)
	assert.ErrorContains(t, err, "begin batch: ")
	assert.EqualValues(t, 1, p.connections.Load())
}
