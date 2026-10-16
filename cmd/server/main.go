package main

import (
	"flag"
	"log"
	"net/http"
	"os"

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
	serverEndpoint := p.address.String()
	lg.Info("listening", "address", serverEndpoint)
	st := storage.NewMemStorage()
	h := handler.NewRouter(st, lg)
	return http.ListenAndServe(serverEndpoint, h)
}
