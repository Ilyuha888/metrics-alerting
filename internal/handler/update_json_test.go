package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func postJSON(s *fakeStorage, target, contentType, body string) *httptest.ResponseRecorder {
	router := handler.NewRouter(s)
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	return w
}

func TestRouter_UpdateJSON(t *testing.T) {
	const jsonType = "application/json"
	tests := []struct {
		name        string
		storage     *fakeStorage
		target      string // "/update" when empty
		contentType string
		body        string
		wantCode    int
		wantBody    string
		wantCalls   []call
	}{
		{
			name: "gauge is stored and echoed", contentType: jsonType,
			body:      `{"id":"temp","type":"gauge","value":36.6}`,
			wantCode:  http.StatusOK,
			wantBody:  `{"id":"temp","type":"gauge","value":36.6}`,
			wantCalls: []call{{kind: metrics.KindGauge, name: "temp", gauge: 36.6}},
		},
		{
			name:    "counter answers the new total",
			storage: &fakeStorage{counters: map[string]metrics.Counter{"hits": 5}}, contentType: jsonType,
			body:      `{"id":"hits","type":"counter","delta":3}`,
			wantCode:  http.StatusOK,
			wantBody:  `{"id":"hits","type":"counter","delta":8}`,
			wantCalls: []call{{kind: metrics.KindCounter, name: "hits", count: 3}},
		},
		{
			name: "charset parameter is accepted", contentType: "application/json; charset=utf-8",
			body:      `{"id":"temp","type":"gauge","value":1}`,
			wantCode:  http.StatusOK,
			wantBody:  `{"id":"temp","type":"gauge","value":1}`,
			wantCalls: []call{{kind: metrics.KindGauge, name: "temp", gauge: 1}},
		},
		{
			name: "trailing slash is the same endpoint", target: "/update/", contentType: jsonType,
			body:      `{"id":"temp","type":"gauge","value":36.6}`,
			wantCode:  http.StatusOK,
			wantBody:  `{"id":"temp","type":"gauge","value":36.6}`,
			wantCalls: []call{{kind: metrics.KindGauge, name: "temp", gauge: 36.6}},
		},
		{name: "two trailing slashes", target: "/update//", contentType: jsonType, body: `{"id":"temp","type":"gauge","value":1}`, wantCode: http.StatusNotFound},
		{name: "text content type", contentType: "text/plain", body: `{"id":"temp","type":"gauge","value":1}`, wantCode: http.StatusUnsupportedMediaType},
		{name: "no content type", body: `{"id":"temp","type":"gauge","value":1}`, wantCode: http.StatusUnsupportedMediaType},
		{name: "empty body", contentType: jsonType, body: "", wantCode: http.StatusBadRequest},
		{name: "malformed JSON", contentType: jsonType, body: `{"id":"temp",`, wantCode: http.StatusBadRequest},
		{name: "unknown field", contentType: jsonType, body: `{"id":"temp","type":"gauge","value":1,"unit":"C"}`, wantCode: http.StatusBadRequest},
		{name: "second object after the first", contentType: jsonType, body: `{"id":"a","type":"gauge","value":1}{"id":"b","type":"gauge","value":2}`, wantCode: http.StatusBadRequest},
		{name: "garbage after the object", contentType: jsonType, body: `{"id":"a","type":"gauge","value":1}}`, wantCode: http.StatusBadRequest},
		{name: "fractional delta", contentType: jsonType, body: `{"id":"hits","type":"counter","delta":1.5}`, wantCode: http.StatusBadRequest},
		{name: "unknown type", contentType: jsonType, body: `{"id":"x","type":"histogram","value":1}`, wantCode: http.StatusBadRequest},
		{name: "gauge without value", contentType: jsonType, body: `{"id":"temp","type":"gauge"}`, wantCode: http.StatusBadRequest},
		{name: "counter without delta", contentType: jsonType, body: `{"id":"hits","type":"counter"}`, wantCode: http.StatusBadRequest},
		{name: "gauge with delta", contentType: jsonType, body: `{"id":"temp","type":"gauge","delta":1}`, wantCode: http.StatusBadRequest},
		{name: "counter with value", contentType: jsonType, body: `{"id":"hits","type":"counter","value":1}`, wantCode: http.StatusBadRequest},
		{name: "gauge with both fields", contentType: jsonType, body: `{"id":"temp","type":"gauge","value":1,"delta":1}`, wantCode: http.StatusBadRequest},
		{name: "empty id", contentType: jsonType, body: `{"id":"","type":"gauge","value":1}`, wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.storage
			if s == nil {
				s = &fakeStorage{}
			}

			target := tt.target
			if target == "" {
				target = "/update"
			}

			w := postJSON(s, target, tt.contentType, tt.body)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Equal(t, tt.wantCalls, s.calls)
			if tt.wantCode == http.StatusOK {
				assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
				assert.JSONEq(t, tt.wantBody, w.Body.String())
			}
		})
	}
}
