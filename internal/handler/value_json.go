package handler

import "net/http"

func (h *handlers) valueJSON(w http.ResponseWriter, r *http.Request) {
	m, ok := decodeMetrics(w, r)
	if !ok {
		return
	}

	loaded, err := h.load(m)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, loaded)
}
