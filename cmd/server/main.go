package main

import (
	"log"
	"net/http"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	st := storage.NewMemStorage()
	h := handler.NewRouter(st)
	return http.ListenAndServe(":8080", h)
}
