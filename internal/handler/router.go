// Package handler serves the metrics HTTP API.
package handler

import (
	"net/http"
	"path"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/go-chi/chi/v5"
)

type Storage interface {
	SetGauge(name string, v metrics.Gauge)
	AddCounter(name string, v metrics.Counter)
	Gauge(name string) (metrics.Gauge, error)
	Counter(name string) (metrics.Counter, error)
	Snapshot() metrics.Snapshot
}

// handlers holds what every endpoint needs, so each one is a plain method.
type handlers struct {
	storage Storage
}

// NewRouter returns the routes; patterns alone answer 404 for a missing name, 405 for a wrong method.
func NewRouter(s Storage) http.Handler {
	h := &handlers{storage: s}

	r := chi.NewRouter()
	r.Use(rejectUncleanPath)

	r.Post("/update/{kind}/{name}/{value}", h.update)
	r.Get("/value/{kind}/{name}", h.value)
	r.Get("/", h.index)
	return r
}

// rejectUncleanPath answers 404 for any non-canonical path. chi reads "//" as an empty name,
// and routes on RawPath, which Go fills only for needless escapes like "%68its" for "hits":
// both would store a report under a name nobody asked for.
func rejectUncleanPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
