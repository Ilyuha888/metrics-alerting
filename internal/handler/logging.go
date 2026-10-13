package handler

import (
	"log/slog"
	"net/http"
	"time"
)

type loggingResponseWriter struct {
	w      http.ResponseWriter
	status int
	size   int
}

var _ http.ResponseWriter = (*loggingResponseWriter)(nil)

func (lw *loggingResponseWriter) Header() http.Header {
	return lw.w.Header()
}

func (lw *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := lw.w.Write(b)
	lw.size += n
	return n, err
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.status = code
	lw.w.WriteHeader(code)
}

func WithLogging(lg *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// A handler that never calls WriteHeader gets 200 from the original writer, so this one never hears it.
		lw := &loggingResponseWriter{w: w, status: http.StatusOK}

		h.ServeHTTP(lw, r)

		lg.Info("request",
			"uri", r.RequestURI,
			"method", r.Method,
			"duration", time.Since(start),
			"status", lw.status,
			"size", lw.size,
		)
	})
}
