package handler

import (
	"math"
	"net/http"
	"strconv"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	m := models.Metrics{ID: r.PathValue("name"), MType: metrics.Kind(r.PathValue("kind"))}
	raw := r.PathValue("value")

	// No default: an unknown kind goes on to save, which owns that check.
	switch m.MType {
	case metrics.KindGauge:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			http.Error(w, "gauge value must be a float64", http.StatusBadRequest)
			return
		}
		// ParseFloat accepts "NaN" and "Inf". JSON cannot encode them, so one stored value would fail every later file save.
		if math.IsNaN(v) || math.IsInf(v, 0) {
			http.Error(w, "gauge value must be a finite float64", http.StatusBadRequest)
			return
		}
		m.Value = &v
	case metrics.KindCounter:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "counter value must be an int64", http.StatusBadRequest)
			return
		}
		m.Delta = &v
	}

	if err := h.save(m); err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}
