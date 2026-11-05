package handler_test

import (
	"bytes"
	"errors"
	"fmt"
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

const (
	jsonType     = "application/json"
	batchLimit   = 1 << 20
	goodElement  = `{"id":"temp","type":"gauge","value":1}`
	otherElement = `{"id":"hits","type":"counter","delta":1}`
)

func postGzipBatch(t *testing.T, s *fakeStorage, body string) *httptest.ResponseRecorder {
	t.Helper()

	router := handler.NewRouter(s, nil, discardLogger)
	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(gzipString(t, body)))
	req.Header.Set("Content-Type", jsonType)
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	return w
}

func paddedInsideList(size int) string {
	return fmt.Sprintf("[%s%s]", goodElement, strings.Repeat(" ", size-len(goodElement)-2))
}

func paddedAfterList(size int) string {
	return fmt.Sprintf("[%s]%s", goodElement, strings.Repeat(" ", size-len(goodElement)-2))
}

func TestRouter_Updates_GoodListStoresOneMergedBatch(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		contentType string
		body        string
		want        metrics.Batch
	}{
		{
			name:   "both kinds through the path with a slash",
			target: "/updates/", contentType: jsonType,
			body: `[{"id":"temp","type":"gauge","value":36.6},{"id":"hits","type":"counter","delta":3}]`,
			want: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 36.6},
				Counters: map[string]metrics.Counter{"hits": 3},
			},
		},
		{
			name:   "the path without a slash is the same endpoint",
			target: "/updates", contentType: jsonType,
			body: `[{"id":"temp","type":"gauge","value":36.6}]`,
			want: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 36.6},
				Counters: map[string]metrics.Counter{},
			},
		},
		{
			name:   "charset parameter is accepted",
			target: "/updates/", contentType: "application/json; charset=utf-8",
			body: `[{"id":"hits","type":"counter","delta":3}]`,
			want: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{},
				Counters: map[string]metrics.Counter{"hits": 3},
			},
		},
		{
			name:   "a repeated gauge takes the last value",
			target: "/updates/", contentType: jsonType,
			body: `[{"id":"temp","type":"gauge","value":1},{"id":"temp","type":"gauge","value":3},{"id":"temp","type":"gauge","value":2}]`,
			want: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 2},
				Counters: map[string]metrics.Counter{},
			},
		},
		{
			name:   "a repeated counter sums",
			target: "/updates/", contentType: jsonType,
			body: `[{"id":"hits","type":"counter","delta":5},{"id":"hits","type":"counter","delta":-2},{"id":"hits","type":"counter","delta":4}]`,
			want: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{},
				Counters: map[string]metrics.Counter{"hits": 7},
			},
		},
		{
			name:   "one name as both kinds stays two metrics",
			target: "/updates/", contentType: jsonType,
			body: `[{"id":"same","type":"gauge","value":1.5},{"id":"same","type":"counter","delta":7}]`,
			want: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"same": 1.5},
				Counters: map[string]metrics.Counter{"same": 7},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}

			w := postJSON(s, tt.target, tt.contentType, tt.body)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Empty(t, w.Body.String())
			assert.Equal(t, []metrics.Batch{tt.want}, s.batches)
			assert.Empty(t, s.calls)
		})
	}
}

func TestRouter_Updates_GzipBodyIsStoredAndTheAnswerStaysEmpty(t *testing.T) {
	s := &fakeStorage{}
	router := handler.NewRouter(s, nil, discardLogger)
	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(gzipString(t, fmt.Sprintf("[%s,%s]", goodElement, otherElement))))
	req.Header.Set("Content-Type", jsonType)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Empty(t, res.Header.Get("Content-Encoding"))
	assert.Empty(t, readBody(t, res))
	assert.Equal(t, []metrics.Batch{{
		Gauges:   map[string]metrics.Gauge{"temp": 1},
		Counters: map[string]metrics.Counter{"hits": 1},
	}}, s.batches)
}

func TestRouter_Updates_EmptyListIsOKWithoutStorage(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty list", body: `[]`},
		{name: "null", body: `null`},
		{name: "empty list with whitespace", body: " [ ]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}

			w := postJSON(s, "/updates/", jsonType, tt.body)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Empty(t, w.Body.String())
			assert.Empty(t, s.ctxErrs)
		})
	}
}

func TestRouter_Updates_BadBodyStoresNothing(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		contentType string
		body        string
		wantCode    int
	}{
		{name: "text content type", contentType: "text/plain", body: `[]`, wantCode: http.StatusUnsupportedMediaType},
		{name: "no content type", body: `[]`, wantCode: http.StatusUnsupportedMediaType},
		{name: "empty body", contentType: jsonType, body: "", wantCode: http.StatusBadRequest},
		{name: "malformed JSON", contentType: jsonType, body: `[{"id":"temp",`, wantCode: http.StatusBadRequest},
		{name: "one object instead of a list", contentType: jsonType, body: goodElement, wantCode: http.StatusBadRequest},
		{name: "second list after the first", contentType: jsonType, body: fmt.Sprintf("[%s][%s]", goodElement, goodElement), wantCode: http.StatusBadRequest},
		{name: "garbage after the list", contentType: jsonType, body: fmt.Sprintf("[%s]]", goodElement), wantCode: http.StatusBadRequest},
		{name: "unknown field in an element", contentType: jsonType, body: `[{"id":"temp","type":"gauge","value":1,"unit":"C"}]`, wantCode: http.StatusBadRequest},
		{name: "fractional delta", contentType: jsonType, body: `[{"id":"hits","type":"counter","delta":1.5}]`, wantCode: http.StatusBadRequest},
		{name: "null element is an empty name", contentType: jsonType, body: `[null]`, wantCode: http.StatusNotFound},
		{name: "two trailing slashes", target: "/updates//", contentType: jsonType, body: `[]`, wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}

			target := tt.target
			if target == "" {
				target = "/updates/"
			}

			w := postJSON(s, target, tt.contentType, tt.body)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Empty(t, s.ctxErrs)
		})
	}
}

func TestRouter_Updates_BadElementInAnyPositionAnswersAsUpdateAndStoresNothing(t *testing.T) {
	tests := []struct {
		name     string
		element  string
		wantCode int
	}{
		{name: "empty id", element: `{"id":"","type":"gauge","value":1}`, wantCode: http.StatusNotFound},
		{name: "no id", element: `{"type":"gauge","value":1}`, wantCode: http.StatusNotFound},
		{name: "NUL in the id", element: `{"id":"a\u0000b","type":"gauge","value":1}`, wantCode: http.StatusBadRequest},
		{name: "unknown type", element: `{"id":"x","type":"histogram","value":1}`, wantCode: http.StatusBadRequest},
		{name: "no type", element: `{"id":"x","value":1}`, wantCode: http.StatusBadRequest},
		{name: "gauge without value", element: `{"id":"temp","type":"gauge"}`, wantCode: http.StatusBadRequest},
		{name: "counter without delta", element: `{"id":"hits","type":"counter"}`, wantCode: http.StatusBadRequest},
		{name: "gauge with delta", element: `{"id":"temp","type":"gauge","delta":1}`, wantCode: http.StatusBadRequest},
		{name: "counter with value", element: `{"id":"hits","type":"counter","value":1}`, wantCode: http.StatusBadRequest},
		{name: "gauge with both fields", element: `{"id":"temp","type":"gauge","value":1,"delta":1}`, wantCode: http.StatusBadRequest},
	}
	positions := []struct {
		name   string
		format string
	}{
		{name: "first", format: "[%[1]s,%[2]s,%[3]s]"},
		{name: "middle", format: "[%[2]s,%[1]s,%[3]s]"},
		{name: "last", format: "[%[2]s,%[3]s,%[1]s]"},
	}
	for _, tt := range tests {
		for _, pos := range positions {
			t.Run(fmt.Sprintf("%s, %s", tt.name, pos.name), func(t *testing.T) {
				s := &fakeStorage{}
				body := fmt.Sprintf(pos.format, tt.element, goodElement, otherElement)

				w := postJSON(s, "/updates/", jsonType, body)
				single := postJSON(&fakeStorage{}, "/update", jsonType, tt.element)

				assert.Equal(t, tt.wantCode, w.Code)
				assert.Equal(t, single.Code, w.Code)
				assert.Equal(t, single.Body.String(), w.Body.String())
				assert.Empty(t, s.ctxErrs)
			})
		}
	}
}

func TestRouter_Updates_StorageFailureIs500WithoutCause(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeStorage{writeErr: errors.New("connection to 10.0.0.1 lost")}
	router := handler.NewRouter(s, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(fmt.Sprintf("[%s]", goodElement)))
	req.Header.Set("Content-Type", jsonType)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "10.0.0.1")
	assert.Contains(t, logs.String(), "connection to 10.0.0.1 lost")
}

func TestRouter_Updates_BodyLimitCountsDecompressedBytes(t *testing.T) {
	manyElements := fmt.Sprintf("[%s%s]", strings.Repeat(goodElement+",", batchLimit/len(goodElement)), goodElement)
	tests := []struct {
		name        string
		body        string
		gzip        bool
		wantCode    int
		wantBatches int
	}{
		{name: "exactly the limit", body: paddedInsideList(batchLimit), wantCode: http.StatusOK, wantBatches: 1},
		{name: "exactly the limit with padding after the list", body: paddedAfterList(batchLimit), wantCode: http.StatusOK, wantBatches: 1},
		{name: "one byte over", body: paddedInsideList(batchLimit + 1), wantCode: http.StatusRequestEntityTooLarge},
		{name: "one byte over with padding after the list", body: paddedAfterList(batchLimit + 1), wantCode: http.StatusRequestEntityTooLarge},
		{name: "many real elements over the limit", body: manyElements, wantCode: http.StatusRequestEntityTooLarge},
		{name: "gzip of exactly the limit", body: paddedInsideList(batchLimit), gzip: true, wantCode: http.StatusOK, wantBatches: 1},
		{name: "small gzip that decompresses one byte over", body: paddedInsideList(batchLimit + 1), gzip: true, wantCode: http.StatusRequestEntityTooLarge},
		{name: "small gzip with padding after the list", body: paddedAfterList(batchLimit + 1), gzip: true, wantCode: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}

			var w *httptest.ResponseRecorder
			if tt.gzip {
				require.Less(t, len(gzipString(t, tt.body)), batchLimit/100)
				w = postGzipBatch(t, s, tt.body)
			} else {
				w = postJSON(s, "/updates/", jsonType, tt.body)
			}

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Len(t, s.batches, tt.wantBatches)
		})
	}
}

func TestRouter_OtherJSONRoutes_HaveNoBodyLimit(t *testing.T) {
	padding := strings.Repeat(" ", 2*batchLimit)
	tests := []struct {
		name   string
		target string
		body   string
	}{
		{name: "update", target: "/update", body: fmt.Sprintf("%s%s", goodElement, padding)},
		{name: "value", target: "/value", body: fmt.Sprintf(`{"id":"temp","type":"gauge"}%s`, padding)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{gauges: map[string]metrics.Gauge{"temp": 1}}

			w := postJSON(s, tt.target, jsonType, tt.body)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}
