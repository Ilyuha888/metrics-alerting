// Package handler serves the metrics HTTP API.
package handler

import (
	"net/http"
	"path"
	"strconv"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

type Storage interface {
	SetGauge(name string, v metrics.Gauge)
	AddCounter(name string, v metrics.Counter)
}

// NewRouter returns the routing tree. The pattern alone answers 404 for a missing name
// or an extra segment, and 405 with an Allow header for another method.
func NewRouter(s Storage) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /update/{kind}/{name}/{value}", update(s))
	return rejectUncleanPath(mux)
}

func update(s Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		raw := r.PathValue("value")

		switch metrics.Kind(r.PathValue("kind")) {
		case metrics.KindGauge:
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				http.Error(w, "gauge value must be a float64", http.StatusBadRequest)
				return
			}
			s.SetGauge(name, metrics.Gauge(v))
		case metrics.KindCounter:
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				http.Error(w, "counter value must be an int64", http.StatusBadRequest)
				return
			}
			s.AddCounter(name, metrics.Counter(v))
		default:
			http.Error(w, "unknown metric type", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	}
}

// rejectUncleanPath answers 404 where ServeMux would redirect: the spec states that
// redirects are not supported.
func rejectUncleanPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Clean(r.URL.Path) != r.URL.Path {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
