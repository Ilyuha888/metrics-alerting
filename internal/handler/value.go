package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

func (h *handlers) value(w http.ResponseWriter, r *http.Request) {
	m, err := h.load(models.Metrics{ID: r.PathValue("name"), MType: metrics.Kind(r.PathValue("kind"))})
	if err != nil {
		h.writeError(w, err)
		return
	}

	switch m.MType {
	case metrics.KindGauge:
		writeText(w, formatGauge(metrics.Gauge(*m.Value)))
	case metrics.KindCounter:
		writeText(w, formatCounter(metrics.Counter(*m.Delta)))
	}
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
