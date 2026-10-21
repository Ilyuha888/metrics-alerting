package handler

import (
	"errors"
	"net/http"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

var (
	errUnknownKind = errors.New("unknown metric type")
	errBadValue    = errors.New("gauge needs value and no delta, counter needs delta and no value")
)

func (h *handlers) save(m models.Metrics) error {
	// An empty name is a 404, as a URL with no name segment is.
	if m.ID == "" {
		return metrics.ErrNotFound
	}

	switch m.MType {
	case metrics.KindGauge:
		if m.Value == nil || m.Delta != nil {
			return errBadValue
		}
		return h.storage.SetGauge(m.ID, metrics.Gauge(*m.Value))
	case metrics.KindCounter:
		if m.Delta == nil || m.Value != nil {
			return errBadValue
		}
		return h.storage.AddCounter(m.ID, metrics.Counter(*m.Delta))
	default:
		return errUnknownKind
	}
}

func (h *handlers) load(m models.Metrics) (models.Metrics, error) {
	if m.ID == "" {
		return models.Metrics{}, metrics.ErrNotFound
	}

	out := models.Metrics{ID: m.ID, MType: m.MType}
	switch m.MType {
	case metrics.KindGauge:
		v, err := h.storage.Gauge(m.ID)
		if err != nil {
			return models.Metrics{}, err
		}
		f := float64(v)
		out.Value = &f
	case metrics.KindCounter:
		v, err := h.storage.Counter(m.ID)
		if err != nil {
			return models.Metrics{}, err
		}
		d := int64(v)
		out.Delta = &d
	default:
		return models.Metrics{}, errUnknownKind
	}
	return out, nil
}

func (h *handlers) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metrics.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, errUnknownKind), errors.Is(err, errBadValue):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		// The client gets a generic text, so the log is the only place the cause shows up.
		h.log.Error("storage failed", "err", err)
		http.Error(w, "storage failed", http.StatusInternalServerError)
	}
}
