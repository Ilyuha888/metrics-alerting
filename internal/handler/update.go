package handler

import (
	"net/http"
	"strconv"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	raw := r.PathValue("value")

	switch metrics.Kind(r.PathValue("kind")) {
	case metrics.KindGauge:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			http.Error(w, "gauge value must be a float64", http.StatusBadRequest)
			return
		}
		h.storage.SetGauge(name, metrics.Gauge(v))
	case metrics.KindCounter:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "counter value must be an int64", http.StatusBadRequest)
			return
		}
		h.storage.AddCounter(name, metrics.Counter(v))
	default:
		http.Error(w, "unknown metric type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}
