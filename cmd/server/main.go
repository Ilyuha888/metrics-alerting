package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Ilyuha888/metrics-alerting/internal/filestore"
	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/logger"
	"github.com/Ilyuha888/metrics-alerting/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	p, err := parseParams(os.Args[1:])

	switch {
	case err == flag.ErrHelp:
		return nil
	case err != nil:
		return err
	}

	lg := logger.New(p.logLevel)
	mem := storage.NewMemStorage()
	st, err := filestore.New(mem, p.filePath, p.storeInterval, lg)
	if err != nil {
		return err
	}
	if p.restore {
		if err := st.Load(); err != nil {
			return fmt.Errorf("restore metrics: %w", err)
		}
	}
	if p.storeInterval > 0 {
		go st.SaveEvery()
	}

	serverEndpoint := p.address.String()
	lg.Info("listening", "address", serverEndpoint, "file", p.filePath, "interval", p.storeInterval, "restore", p.restore)
	h := handler.NewRouter(st, lg)
	return http.ListenAndServe(serverEndpoint, h)
}
