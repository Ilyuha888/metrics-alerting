package handler_test

import (
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func TestRouter_ValueJSON(t *testing.T) {
	const jsonType = "application/json"
	tests := []struct {
		name        string
		storage     *fakeStorage
		target      string // "/value" when empty
		contentType string
		body        string
		wantCode    int
		wantBody    string
	}{
		{
			name: "stored gauge", storage: storedMetrics(), contentType: jsonType,
			body:     `{"id":"temp","type":"gauge"}`,
			wantCode: http.StatusOK,
			wantBody: `{"id":"temp","type":"gauge","value":36.6}`,
		},
		{
			name: "stored counter", storage: storedMetrics(), contentType: jsonType,
			body:     `{"id":"hits","type":"counter"}`,
			wantCode: http.StatusOK,
			wantBody: `{"id":"hits","type":"counter","delta":527}`,
		},
		{
			name: "counter equal to zero keeps its delta", storage: storedMetrics(), contentType: jsonType,
			body:     `{"id":"zero","type":"counter"}`,
			wantCode: http.StatusOK,
			wantBody: `{"id":"zero","type":"counter","delta":0}`,
		},
		{
			name: "trailing slash is the same endpoint", storage: storedMetrics(), target: "/value/", contentType: jsonType,
			body:     `{"id":"temp","type":"gauge"}`,
			wantCode: http.StatusOK,
			wantBody: `{"id":"temp","type":"gauge","value":36.6}`,
		},
		{name: "missing metric", storage: storedMetrics(), contentType: jsonType, body: `{"id":"nope","type":"gauge"}`, wantCode: http.StatusNotFound},
		{name: "name exists only as the other kind", storage: storedMetrics(), contentType: jsonType, body: `{"id":"shared","type":"counter"}`, wantCode: http.StatusNotFound},
		{name: "empty id", storage: storedMetrics(), contentType: jsonType, body: `{"id":"","type":"gauge"}`, wantCode: http.StatusNotFound},
		{name: "unknown type", storage: storedMetrics(), contentType: jsonType, body: `{"id":"temp","type":"histogram"}`, wantCode: http.StatusBadRequest},
		{name: "text content type", storage: storedMetrics(), contentType: "text/plain", body: `{"id":"temp","type":"gauge"}`, wantCode: http.StatusUnsupportedMediaType},
		{name: "unknown field", storage: storedMetrics(), contentType: jsonType, body: `{"id":"temp","type":"gauge","unit":"C"}`, wantCode: http.StatusBadRequest},
		{name: "storage failure", storage: &fakeStorage{readErr: errors.New("disk on fire")}, contentType: jsonType, body: `{"id":"temp","type":"gauge"}`, wantCode: http.StatusInternalServerError},
		{
			name:    "NaN gauge cannot be encoded",
			storage: &fakeStorage{gauges: map[string]metrics.Gauge{"nan": metrics.Gauge(math.NaN())}}, contentType: jsonType,
			body:     `{"id":"nan","type":"gauge"}`,
			wantCode: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := tt.target
			if target == "" {
				target = "/value"
			}

			w := postJSON(tt.storage, target, tt.contentType, tt.body)

			assert.Equal(t, tt.wantCode, w.Code)
			if tt.wantCode == http.StatusOK {
				assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
				assert.JSONEq(t, tt.wantBody, w.Body.String())
			} else {
				assert.NotEqual(t, "application/json", w.Header().Get("Content-Type"), "an error is never labelled as JSON")
			}
		})
	}
}
