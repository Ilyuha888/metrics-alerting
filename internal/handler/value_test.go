package handler_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
)

// storedMetrics is a fresh fake per case, so one case cannot leak state into the next.
func storedMetrics() *fakeStorage {
	return &fakeStorage{
		gauges:   map[string]metrics.Gauge{"temp": 36.6, "Sys": 1234567800, "shared": 1.5},
		counters: map[string]metrics.Counter{"hits": 527, "zero": 0},
	}
}

func TestRouter_Value(t *testing.T) {
	tests := []struct {
		name     string
		storage  *fakeStorage
		method   string
		target   string
		wantCode int
		wantBody string
	}{
		{name: "gauge reads back as sent", storage: storedMetrics(), method: http.MethodGet, target: "/value/gauge/temp", wantCode: http.StatusOK, wantBody: "36.6"},
		{name: "large gauge has no exponent", storage: storedMetrics(), method: http.MethodGet, target: "/value/gauge/Sys", wantCode: http.StatusOK, wantBody: "1234567800"},
		{name: "counter", storage: storedMetrics(), method: http.MethodGet, target: "/value/counter/hits", wantCode: http.StatusOK, wantBody: "527"},
		{name: "counter equal to zero is found", storage: storedMetrics(), method: http.MethodGet, target: "/value/counter/zero", wantCode: http.StatusOK, wantBody: "0"},
		{name: "unknown gauge", storage: storedMetrics(), method: http.MethodGet, target: "/value/gauge/nope", wantCode: http.StatusNotFound},
		{name: "unknown counter", storage: storedMetrics(), method: http.MethodGet, target: "/value/counter/nope", wantCode: http.StatusNotFound},
		{name: "name exists only as the other kind", storage: storedMetrics(), method: http.MethodGet, target: "/value/counter/shared", wantCode: http.StatusNotFound},
		{name: "unknown kind", storage: storedMetrics(), method: http.MethodGet, target: "/value/histogram/temp", wantCode: http.StatusBadRequest},
		{name: "missing name", storage: storedMetrics(), method: http.MethodGet, target: "/value/gauge", wantCode: http.StatusNotFound},
		{name: "unclean path", storage: storedMetrics(), method: http.MethodGet, target: "/value/gauge/temp/", wantCode: http.StatusNotFound},
		{name: "needlessly escaped name", storage: storedMetrics(), method: http.MethodGet, target: "/value/gauge/%74emp", wantCode: http.StatusNotFound},
		{name: "storage failure", storage: &fakeStorage{readErr: errors.New("disk on fire")}, method: http.MethodGet, target: "/value/gauge/temp", wantCode: http.StatusInternalServerError},
		{name: "wrong method", storage: storedMetrics(), method: http.MethodPost, target: "/value/gauge/temp", wantCode: http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := handler.NewRouter(tt.storage)
			req := httptest.NewRequest(tt.method, tt.target, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
			if tt.wantCode == http.StatusOK {
				assert.Equal(t, tt.wantBody, w.Body.String())
				assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
			}
		})
	}
}
