// Package database opens the PostgreSQL pool and brings its schema up to date.
package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/retry"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Inside the binary, so the server finds its migrations from any working directory.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Open returns a pool that answered a ping and has every migration applied. The server cannot
// serve without its tables, so the caller gets a ready pool or an error, never a half-ready one.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	return open(ctx, dsn, retry.DefaultDelays)
}

func open(ctx context.Context, dsn string, delays []time.Duration) (*sql.DB, error) {
	// pgx's text is dropped: it prints the string as is when it does not recognise the password
	// syntax, and a one-character typo is enough to put the password in the log.
	if _, err := pgconn.ParseConfig(dsn); err != nil {
		return nil, errors.New("invalid DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := prepare(ctx, db, delays); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func prepare(ctx context.Context, db *sql.DB, delays []time.Duration) error {
	ping := func() error { return db.PingContext(ctx) }
	if err := retry.Do(ctx, delays, IsRetriable, ping); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	// No provider.Close: it closes the db given to NewProvider, the same pool we return to serve from.
	provider, err := newProvider(db)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func newProvider(db *sql.DB) (*goose.Provider, error) {
	// goose looks for .sql files at the root of the FS it gets.
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, files)
}

// IsRetriable reports the failures every method repeats: the connection never came up, or the server
// reports a connection exception. 08007 is out, since it says a commit's result is unknown. A connection
// lost later is a different case, see IsConnectionLost.
func IsRetriable(err error) bool {
	// First, because a wrong password or 53300 arrives as a ConnectError wrapping a PgError.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == pgerrcode.TransactionResolutionUnknown {
			return false
		}
		return pgerrcode.IsConnectionException(pgErr.Code) || pgErr.Code == pgerrcode.CannotConnectNow
	}
	// The connection never came up, so the statement was not sent.
	var connectErr *pgconn.ConnectError
	return errors.As(err, &connectErr)
}

// IsConnectionLost reports a connection that was up and went away. 57P01 can follow a commit whose answer
// was lost, so this repeats only where a repeat cannot add a counter twice: the upsert sums. By its
// contract driver.ErrBadConn means the server did not perform the operation; it takes the same care.
func IsConnectionLost(err error) bool {
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.AdminShutdown
}
