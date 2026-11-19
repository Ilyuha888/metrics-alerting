package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/logger"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/database"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/filestore"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/memstorage"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/pgstore"
)

// shutdownTimeout bounds the wait for requests in flight after a signal.
const shutdownTimeout = 10 * time.Second

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

	// From here on a signal no longer kills the process: it only cancels ctx, so every step below
	// that can wait takes ctx, or the server would ignore SIGTERM until it finished.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	const startupTimeout = 10 * time.Second

	var st handler.Storage
	// An interface, so it stays truly nil without a database. A nil *sql.DB stored in an interface
	// does not compare equal to nil, and the handler's nil check would miss it.
	var pinger handler.Pinger
	var fileStore *filestore.Storage
	var saving sync.WaitGroup
	var storageKind string
	switch {
	case p.databaseDSN != "":
		openCtx, cancel := context.WithTimeout(ctx, startupTimeout)
		defer cancel()
		db, err := database.Open(openCtx, p.databaseDSN)
		if err != nil && ctx.Err() != nil {
			// A signal during the startup retries is a stop by signal, so it exits 0 like any other.
			lg.Info("stopped during startup", "err", err)
			return nil
		}
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		// Deferred, so the pool closes after Shutdown has waited for the requests that use it.
		defer db.Close()
		st, pinger = pgstore.New(db), db
		storageKind = "database"
	case p.filePath != "":
		fileStore, err = filestore.New(memstorage.New(), p.filePath, p.storeInterval, p.restore, lg)
		if err != nil {
			return err
		}
		if p.storeInterval > 0 {
			saving.Go(func() { fileStore.SaveEvery(ctx) })
		}
		st = fileStore
		storageKind = "file"
	default:
		st = memstorage.New()
		storageKind = "memory"
	}

	serverEndpoint := p.address.String()
	lg.Info("listening", "address", serverEndpoint, "storage", storageKind, "file", p.filePath, "interval", p.storeInterval, "restore", p.restore)
	srv := &http.Server{Addr: serverEndpoint, Handler: handler.NewRouter(st, pinger, lg, []byte(p.key))}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	// A second signal now kills the process at once, for a stop that hangs.
	stop()
	lg.Info("shutting down", "timeout", shutdownTimeout)

	// The signal's ctx is already done, so the wait needs a context of its own.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		lg.Error("requests still running at the shutdown timeout, closing their connections", "err", err)
		srv.Close()
	}
	// A listen error can race the signal; Shutdown has made ListenAndServe return, so this does not block.
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// SaveEvery has returned before the last save, so two saves never overlap and this one comes after
	// every write that Shutdown waited for.
	saving.Wait()
	if fileStore != nil {
		if err := fileStore.Save(); err != nil {
			return fmt.Errorf("last save: %w", err)
		}
	}
	lg.Info("server stopped")
	return nil
}
