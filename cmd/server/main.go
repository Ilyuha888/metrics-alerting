package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Ilyuha888/metrics-alerting/internal/filestore"
	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/logger"
	"github.com/Ilyuha888/metrics-alerting/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	p, err := parseParams(os.Args[1:])

	switch {
	case errors.Is(err, flag.ErrHelp):
		return nil
	case err != nil:
		return err
	}

	lg := logger.New(p.logLevel)
	mem := storage.NewMemStorage()
	st, err := filestore.New(mem, p.filePath, p.storeInterval, p.restore, lg)
	if err != nil {
		return err
	}
	if p.storeInterval > 0 {
		go st.SaveEvery()
	}

	// TODO: move opening the database to its own package once metrics are stored in Postgres.
	// An interface, not *sql.DB: a nil pointer stored in it would compare non-nil in the handler.
	var pinger handler.Pinger
	if p.databaseDSN != "" {
		db, err := sql.Open("pgx", p.databaseDSN)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()
		pinger = db
	}

	serverEndpoint := p.address.String()
	lg.Info("listening", "address", serverEndpoint, "file", p.filePath, "interval", p.storeInterval, "restore", p.restore)
	h := handler.NewRouter(st, pinger, lg)
	return http.ListenAndServe(serverEndpoint, h)
}
