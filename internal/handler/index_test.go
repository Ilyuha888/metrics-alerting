package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getIndex(t *testing.T, s *fakeStorage) string {
	t.Helper()
	router := handler.NewRouter(s)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	return w.Body.String()
}

func TestRouter_Index_ListsBothKindsSorted(t *testing.T) {
	body := getIndex(t, &fakeStorage{
		gauges:   map[string]metrics.Gauge{"b_gauge": 2.5, "a_gauge": 1},
		counters: map[string]metrics.Counter{"PollCount": 7},
	})

	for _, want := range []string{"a_gauge: 1", "b_gauge: 2.5", "PollCount: 7"} {
		assert.Contains(t, body, want)
	}
	assert.Less(t, strings.Index(body, "a_gauge"), strings.Index(body, "b_gauge"), "gauges are sorted by name")
	assert.Less(t, strings.Index(body, "Gauges"), strings.Index(body, "Counters"), "gauges come first")
	assert.Less(t, strings.Index(body, "b_gauge"), strings.Index(body, "Counters"), "each kind sits in its own block")
}

func TestRouter_Index_EscapesNames(t *testing.T) {
	body := getIndex(t, &fakeStorage{
		gauges: map[string]metrics.Gauge{"<script>alert(1)</script>": 1},
	})

	assert.NotContains(t, body, "<script>")
	assert.Contains(t, body, "&lt;script&gt;")
}

func TestRouter_Index_EmptyStorage(t *testing.T) {
	body := getIndex(t, &fakeStorage{})

	assert.Equal(t, 2, strings.Count(body, "<li>none</li>"))
}
