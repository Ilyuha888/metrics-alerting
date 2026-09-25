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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{}
			router := handler.NewRouter(fake)
			req := httptest.NewRequest(tt.method, tt.target, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Equal(t, tt.wantCalls, fake.calls)
		})
	}
}

func TestRouter_Update_AllowHeaderOnWrongMethod(t *testing.T) {
	router := handler.NewRouter(&fakeStorage{})
	req := httptest.NewRequest(http.MethodGet, "/update/counter/hits/1", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, http.MethodPost, w.Header().Get("Allow"))
}
