// Package database opens the PostgreSQL pool and brings its schema up to date.
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

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
	// pgx's text is dropped: it prints the string as is when it does not recognise the password
	// syntax, and a one-character typo is enough to put the password in the log.
	if _, err := pgconn.ParseConfig(dsn); err != nil {
		return nil, errors.New("invalid DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := prepare(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func prepare(ctx context.Context, db *sql.DB) error {
	if err := db.PingContext(ctx); err != nil {
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
