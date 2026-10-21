package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_Update(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		target    string
		wantCode  int
		wantCalls []call
	}{
		{
			name: "gauge is stored", method: http.MethodPost, target: "/update/gauge/temp/36.6",
			wantCode:  http.StatusOK,
			wantCalls: []call{{kind: metrics.KindGauge, name: "temp", gauge: 36.6}},
		},
		{
			name: "counter is stored", method: http.MethodPost, target: "/update/counter/hits/527",
			wantCode:  http.StatusOK,
			wantCalls: []call{{kind: metrics.KindCounter, name: "hits", count: 527}},
		},
		{
			name: "negative gauge", method: http.MethodPost, target: "/update/gauge/delta/-1.5",
			wantCode:  http.StatusOK,
			wantCalls: []call{{kind: metrics.KindGauge, name: "delta", gauge: -1.5}},
		},
		{
			name: "missing name", method: http.MethodPost, target: "/update/counter/527",
			wantCode: http.StatusNotFound,
		},
		{
			name: "extra segment", method: http.MethodPost, target: "/update/counter/hits/1/extra",
			wantCode: http.StatusNotFound,
		},
		{
			name: "empty value", method: http.MethodPost, target: "/update/counter/hits/",
			wantCode: http.StatusNotFound,
		},
		{
			name: "double slash is not redirected", method: http.MethodPost, target: "/update/counter//527",
			wantCode: http.StatusNotFound,
		},
		{
			name: "double slash before the value writes nothing", method: http.MethodPost, target: "/update/counter/hits//1",
			wantCode: http.StatusNotFound,
		},
		{
			name: "double slash before the name writes nothing", method: http.MethodPost, target: "/update/counter//hits/1",
			wantCode: http.StatusNotFound,
		},
		{
			name: "dot segments write nothing", method: http.MethodPost, target: "/update/counter/x/../hits/1",
			wantCode: http.StatusNotFound,
		},
		{
			name: "trailing slash writes nothing", method: http.MethodPost, target: "/update/counter/hits/1/",
			wantCode: http.StatusNotFound,
		},
		{
			name: "trailing slash after a gauge value writes nothing", method: http.MethodPost, target: "/update/gauge/x/1/",
			wantCode: http.StatusNotFound,
		},
		{
			name: "root double slash", method: http.MethodGet, target: "//",
			wantCode: http.StatusNotFound,
		},
		{
			name: "needlessly escaped name writes nothing", method: http.MethodPost, target: "/update/counter/%68its/1",
			wantCode: http.StatusNotFound,
		},
		{
			name: "escaped slash in the name writes nothing", method: http.MethodPost, target: "/update/counter/a%2Fb/1",
			wantCode: http.StatusNotFound,
		},
		{
			name: "standard escape is decoded", method: http.MethodPost, target: "/update/gauge/a%20b/1",
			wantCode:  http.StatusOK,
			wantCalls: []call{{kind: metrics.KindGauge, name: "a b", gauge: 1}},
		},
		{
			name: "unknown path", method: http.MethodPost, target: "/ping",
			wantCode: http.StatusNotFound,
		},
		{
			name: "wrong method", method: http.MethodGet, target: "/update/counter/hits/527",
			wantCode: http.StatusMethodNotAllowed,
		},
		{
			name: "unknown kind", method: http.MethodPost, target: "/update/histogram/x/1",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "gauge value is not a number", method: http.MethodPost, target: "/update/gauge/temp/warm",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "counter value is fractional", method: http.MethodPost, target: "/update/counter/hits/1.5",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "gauge value overflows float64", method: http.MethodPost, target: "/update/gauge/temp/1e999",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "gauge value NaN is rejected", method: http.MethodPost, target: "/update/gauge/temp/NaN",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "gauge value Inf is rejected", method: http.MethodPost, target: "/update/gauge/temp/Inf",
			wantCode: http.StatusBadRequest,
		},
		{
			name: "gauge value -Inf is rejected", method: http.MethodPost, target: "/update/gauge/temp/-Inf",
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{}
			router := handler.NewRouter(fake, discardLogger)
			req := httptest.NewRequest(tt.method, tt.target, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Equal(t, tt.wantCalls, fake.calls)
		})
	}
}

func TestRouter_Update_AllowHeaderOnWrongMethod(t *testing.T) {
	router := handler.NewRouter(&fakeStorage{}, discardLogger)
	req := httptest.NewRequest(http.MethodGet, "/update/counter/hits/1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, http.MethodPost, w.Header().Get("Allow"))
}

func TestRouter_Update_FailedWriteAnswers500AndLogsTheCause(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		contentType string
		body        string
	}{
		{name: "gauge by URL", target: "/update/gauge/temp/36.6"},
		{name: "counter by URL", target: "/update/counter/hits/5"},
		{name: "gauge by JSON", target: "/update", contentType: "application/json", body: `{"id":"temp","type":"gauge","value":36.6}`},
		{name: "counter by JSON", target: "/update", contentType: "application/json", body: `{"id":"hits","type":"counter","delta":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&buf, nil))
			fake := &fakeStorage{writeErr: errors.New("disk full")}
			router := handler.NewRouter(fake, lg)
			req := httptest.NewRequest(http.MethodPost, tt.target, strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Equal(t, "storage failed\n", w.Body.String())
			var errorEntries []map[string]any
			for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
				var entry map[string]any
				require.NoError(t, json.Unmarshal(line, &entry))
				if entry["level"] == "ERROR" {
					errorEntries = append(errorEntries, entry)
				}
			}
			require.Len(t, errorEntries, 1)
			assert.Equal(t, "storage failed", errorEntries[0]["msg"])
			assert.Equal(t, "disk full", errorEntries[0]["err"])
		})
	}
}
