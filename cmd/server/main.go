package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	f, parsErr := parseFlags(os.Args[1:])

	switch {
	case parsErr == flag.ErrHelp:
		return nil
	case parsErr != nil:
		return parsErr
	}

	fmt.Printf("Listening on %s\n", &f.address)
	serverEndpoint := f.address.String()
	st := storage.NewMemStorage()
	h := handler.NewRouter(st)
	return http.ListenAndServe(serverEndpoint, h)
}
