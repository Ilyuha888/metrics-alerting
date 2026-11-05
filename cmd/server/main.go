package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/logger"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/database"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/filestore"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/memstorage"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/pgstore"
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

	const startupTimeout = 10 * time.Second

	var st handler.Storage
	// An interface, so it stays truly nil without a database. A nil *sql.DB stored in an interface
	// does not compare equal to nil, and the handler's nil check would miss it.
	var pinger handler.Pinger
	var storageKind string
	switch {
	case p.databaseDSN != "":
		ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
		defer cancel()
		db, err := database.Open(ctx, p.databaseDSN)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()
		st, pinger = pgstore.New(db), db
		storageKind = "database"
	case p.filePath != "":
		fileStore, err := filestore.New(memstorage.New(), p.filePath, p.storeInterval, p.restore, lg)
		if err != nil {
			return err
		}
		if p.storeInterval > 0 {
			go fileStore.SaveEvery()
		}
		st = fileStore
		storageKind = "file"
	default:
		st = memstorage.New()
		storageKind = "memory"
	}

	serverEndpoint := p.address.String()
	lg.Info("listening", "address", serverEndpoint, "storage", storageKind, "file", p.filePath, "interval", p.storeInterval, "restore", p.restore)
	h := handler.NewRouter(st, pinger, lg)
	return http.ListenAndServe(serverEndpoint, h)
}
