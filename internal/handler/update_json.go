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
	var m models.Metrics
	if !decodeJSON(w, r, &m) {
		return
	}

	if err := h.save(r.Context(), m); err != nil {
		h.writeError(w, err)
		return
	}
	// Read back rather than echo the request: a counter answers its new total.
	saved, err := h.load(r.Context(), m)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, saved)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// ParseMediaType, so "application/json; charset=utf-8" passes too.
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		rejectBody(w, err, fmt.Sprintf("invalid JSON body: %v", err))
		return false
	}
	// Decode stops after the first value; only io.EOF proves nothing follows it.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		rejectBody(w, err, "invalid JSON body: data after the JSON value")
		return false
	}
	return true
}

// rejectBody answers 413 when a size limit stopped the read, since the JSON may be fine; the
// limit can also trip after the first value, on padding that follows it.
func rejectBody(w http.ResponseWriter, err error, invalid string) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, fmt.Sprintf("body is over %d bytes", tooLarge.Limit), http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, invalid, http.StatusBadRequest)
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
