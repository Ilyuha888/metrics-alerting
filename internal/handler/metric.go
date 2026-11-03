package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

var (
	errUnknownKind = errors.New("unknown metric type")
	errBadValue    = errors.New("gauge needs value and no delta, counter needs delta and no value")
	errBadName     = errors.New("invalid metric name: must be valid UTF-8 without NUL bytes")
)

func (h *handlers) save(ctx context.Context, m models.Metrics) error {
	// An empty name is a 404, as a URL with no name segment is.
	if m.ID == "" {
		return metrics.ErrNotFound
	}
	if !validName(m.ID) {
		return errBadName
	}

	switch m.MType {
	case metrics.KindGauge:
		if m.Value == nil || m.Delta != nil {
			return errBadValue
		}
		return h.storage.SetGauge(ctx, m.ID, metrics.Gauge(*m.Value))
	case metrics.KindCounter:
		if m.Delta == nil || m.Value != nil {
			return errBadValue
		}
		return h.storage.AddCounter(ctx, m.ID, metrics.Counter(*m.Delta))
	default:
		return errUnknownKind
	}
}

func (h *handlers) load(ctx context.Context, m models.Metrics) (models.Metrics, error) {
	if m.ID == "" {
		return models.Metrics{}, metrics.ErrNotFound
	}
	if !validName(m.ID) {
		return models.Metrics{}, errBadName
	}

	out := models.Metrics{ID: m.ID, MType: m.MType}
	switch m.MType {
	case metrics.KindGauge:
		v, err := h.storage.Gauge(ctx, m.ID)
		if err != nil {
			return models.Metrics{}, err
		}
		f := float64(v)
		out.Value = &f
	case metrics.KindCounter:
		v, err := h.storage.Counter(ctx, m.ID)
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

// validName holds in every mode, so a name accepted in memory also fits a Postgres TEXT column.
func validName(name string) bool {
	return utf8.ValidString(name) && !strings.ContainsRune(name, 0)
}

func (h *handlers) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metrics.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, errUnknownKind), errors.Is(err, errBadValue), errors.Is(err, errBadName):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		// The client gets a generic text, so the log is the only place the cause shows up.
		h.log.Error("storage failed", "err", err)
		http.Error(w, "storage failed", http.StatusInternalServerError)
	}
}
