package handler

import (
	"context"
	"net/http"
	"time"
)

const pingTimeout = 3 * time.Second

func (h *handlers) ping(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		// The client gets a generic text, so the log is the only place the cause shows up.
		h.log.Error("ping database", "err", err)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
