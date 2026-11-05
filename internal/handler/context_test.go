package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_Storage_GetsRequestContext(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		target    string
		body      string
		wantCalls int
	}{
		{name: "update gauge from the URL", method: http.MethodPost, target: "/update/gauge/temp/1.5", wantCalls: 1},
		{name: "update counter from the URL", method: http.MethodPost, target: "/update/counter/hits/1", wantCalls: 1},
		{name: "update gauge as JSON saves and loads", method: http.MethodPost, target: "/update", body: `{"id":"temp","type":"gauge","value":1.5}`, wantCalls: 2},
		{name: "update counter as JSON saves and loads", method: http.MethodPost, target: "/update", body: `{"id":"hits","type":"counter","delta":1}`, wantCalls: 2},
		{name: "batch as JSON", method: http.MethodPost, target: "/updates/", body: `[{"id":"temp","type":"gauge","value":1.5}]`, wantCalls: 1},
		{name: "value of a gauge from the URL", method: http.MethodGet, target: "/value/gauge/temp", wantCalls: 1},
		{name: "value of a counter from the URL", method: http.MethodGet, target: "/value/counter/hits", wantCalls: 1},
		{name: "value of a gauge as JSON", method: http.MethodPost, target: "/value", body: `{"id":"temp","type":"gauge"}`, wantCalls: 1},
		{name: "value of a counter as JSON", method: http.MethodPost, target: "/value", body: `{"id":"hits","type":"counter"}`, wantCalls: 1},
		{name: "index", method: http.MethodGet, target: "/", wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{
				gauges:   map[string]metrics.Gauge{"temp": 1.5},
				counters: map[string]metrics.Counter{"hits": 1},
			}
			router := handler.NewRouter(fake, nil, discardLogger)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(httptest.NewRecorder(), req)

			require.Len(t, fake.ctxErrs, tt.wantCalls)
			for _, err := range fake.ctxErrs {
				assert.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}
