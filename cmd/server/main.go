package main

import (
	"errors"
	"flag"
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

	serverEndpoint := p.address.String()
	lg.Info("listening", "address", serverEndpoint, "file", p.filePath, "interval", p.storeInterval, "restore", p.restore)
	h := handler.NewRouter(st, lg)
	return http.ListenAndServe(serverEndpoint, h)
}
