package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

func (h *handlers) updateJSON(w http.ResponseWriter, r *http.Request) {
	m, ok := decodeMetrics(w, r)
	if !ok {
		return
	}

	if err := h.save(m); err != nil {
		h.writeError(w, err)
		return
	}
	// Read back rather than echo the request: a counter answers its new total.
	saved, err := h.load(m)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, saved)
}

func decodeMetrics(w http.ResponseWriter, r *http.Request) (models.Metrics, bool) {
	// ParseMediaType, so "application/json; charset=utf-8" passes too.
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return models.Metrics{}, false
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var m models.Metrics
	if err := dec.Decode(&m); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return models.Metrics{}, false
	}
	// Decode stops after the first value; only io.EOF proves nothing follows it.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid JSON body: data after the object", http.StatusBadRequest)
		return models.Metrics{}, false
	}
	return m, true
}

func writeJSON(w http.ResponseWriter, m models.Metrics) {
	// Marshal before any header is set: a NaN gauge fails here and still gets a clean 500.
	body, err := json.Marshal(m)
	if err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
