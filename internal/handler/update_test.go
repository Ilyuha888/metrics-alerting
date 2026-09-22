// External test package: reach only what a real caller can, so everything goes through
// NewRouter.
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type call struct {
	kind  metrics.Kind
	name  string
	gauge metrics.Gauge
	count metrics.Counter
}

type spyStorage struct{ calls []call }

func (s *spyStorage) SetGauge(name string, v metrics.Gauge) {
	s.calls = append(s.calls, call{kind: metrics.KindGauge, name: name, gauge: v})
}

func (s *spyStorage) AddCounter(name string, v metrics.Counter) {
	s.calls = append(s.calls, call{kind: metrics.KindCounter, name: name, count: v})
}

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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &spyStorage{}
			w := httptest.NewRecorder()

			handler.NewRouter(spy).ServeHTTP(w, httptest.NewRequest(tt.method, tt.target, nil))

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Equal(t, tt.wantCalls, spy.calls)
		})
	}
}

func TestRouter_Update_AllowHeaderOnWrongMethod(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/update/counter/hits/1", nil)

	handler.NewRouter(&spyStorage{}).ServeHTTP(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, http.MethodPost, w.Header().Get("Allow"))
}
