package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

func (h *handlers) value(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	switch metrics.Kind(r.PathValue("kind")) {
	case metrics.KindGauge:
		v, err := h.storage.Gauge(name)
		if err != nil {
			writeReadError(w, err)
			return
		}
		writeText(w, formatGauge(v))
	case metrics.KindCounter:
		v, err := h.storage.Counter(name)
		if err != nil {
			writeReadError(w, err)
			return
		}
		writeText(w, formatCounter(v))
	default:
		http.Error(w, "unknown metric type", http.StatusBadRequest)
	}
}

func writeReadError(w http.ResponseWriter, err error) {
	if err == metrics.ErrNotFound {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, "storage read failed", http.StatusInternalServerError)
}

func writeText(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// A write error means the client is gone.
	_, _ = io.WriteString(w, s)
}

// formatGauge prints the shortest form that parses back: 36.6, not 36.600000.
func formatGauge(v metrics.Gauge) string {
	return strconv.FormatFloat(float64(v), 'f', -1, 64)
}

func formatCounter(v metrics.Counter) string {
	return strconv.FormatInt(int64(v), 10)
}
