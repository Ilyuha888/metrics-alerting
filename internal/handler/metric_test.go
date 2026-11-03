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

func TestRouter_BadName_Is400AndStorageIsNotCalled(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "update counter with NUL from the URL", method: http.MethodPost, target: "/update/counter/a%00b/1"},
		{name: "update gauge with NUL from the URL", method: http.MethodPost, target: "/update/gauge/a%00b/1"},
		{name: "update counter with invalid UTF-8 from the URL", method: http.MethodPost, target: "/update/counter/a%FFb/1"},
		{name: "update gauge with invalid UTF-8 from the URL", method: http.MethodPost, target: "/update/gauge/a%FFb/1"},
		{name: "value of a counter with NUL from the URL", method: http.MethodGet, target: "/value/counter/a%00b"},
		{name: "value of a gauge with NUL from the URL", method: http.MethodGet, target: "/value/gauge/a%00b"},
		{name: "value of a counter with invalid UTF-8 from the URL", method: http.MethodGet, target: "/value/counter/a%FFb"},
		{name: "value of a gauge with invalid UTF-8 from the URL", method: http.MethodGet, target: "/value/gauge/a%FFb"},
		{name: "update counter with NUL as JSON", method: http.MethodPost, target: "/update/", body: `{"id":"a\u0000b","type":"counter","delta":1}`},
		{name: "update gauge with NUL as JSON", method: http.MethodPost, target: "/update/", body: `{"id":"a\u0000b","type":"gauge","value":1}`},
		{name: "value of a counter with NUL as JSON", method: http.MethodPost, target: "/value/", body: `{"id":"a\u0000b","type":"counter"}`},
		{name: "value of a gauge with NUL as JSON", method: http.MethodPost, target: "/value/", body: `{"id":"a\u0000b","type":"gauge"}`},
		{name: "unknown kind still reports the name first", method: http.MethodGet, target: "/value/histogram/a%00b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{}
			router := handler.NewRouter(fake, nil, discardLogger)
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, "invalid metric name: must be valid UTF-8 without NUL bytes\n", w.Body.String())
			assert.Empty(t, fake.ctxErrs, "storage must not be reached")
		})
	}
}

func TestRouter_UnusualValidName_ReachesStorage(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "non-ASCII letter in the update URL", method: http.MethodPost, target: "/update/counter/a%C3%BFb/1"},
		{name: "non-ASCII letter in the value URL", method: http.MethodGet, target: "/value/counter/a%C3%BFb"},
		{name: "replacement character as JSON", method: http.MethodPost, target: "/value/", body: `{"id":"a�b","type":"counter"}`},
		{name: "control character other than NUL as JSON", method: http.MethodPost, target: "/value/", body: `{"id":"a\u0001b","type":"counter"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{counters: map[string]metrics.Counter{"aÿb": 1, "a�b": 1, "a\x01b": 1}}
			router := handler.NewRouter(fake, nil, discardLogger)
			req := httptest.NewRequest(tt.method, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Len(t, fake.ctxErrs, 1)
		})
	}
}
