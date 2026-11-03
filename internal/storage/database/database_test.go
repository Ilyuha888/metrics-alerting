// Internal test package: the up, down, up test needs newProvider and the embedded files.
package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

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
