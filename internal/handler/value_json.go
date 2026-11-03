package handler

import (
	"net/http"

	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

func (h *handlers) valueJSON(w http.ResponseWriter, r *http.Request) {
	var m models.Metrics
	if !decodeJSON(w, r, &m) {
		return
	}

	loaded, err := h.load(r.Context(), m)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, loaded)
}
