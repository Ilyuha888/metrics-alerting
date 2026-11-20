package handler_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/Ilyuha888/metrics-alerting/internal/sign"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/memstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	jsonType     = "application/json"
	batchLimit   = 1 << 20
	goodElement  = `{"id":"temp","type":"gauge","value":1}`
	otherElement = `{"id":"hits","type":"counter","delta":1}`
	goodListBody = `[{"id":"temp","type":"gauge","value":1},{"id":"hits","type":"counter","delta":1}]`
)

func postGzipBatch(t *testing.T, s *fakeStorage, body string) *httptest.ResponseRecorder {
	t.Helper()

	router := handler.NewRouter(s, nil, discardLogger, nil)
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
	router := handler.NewRouter(s, nil, discardLogger, nil)
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
	router := handler.NewRouter(s, nil, slog.New(slog.NewTextHandler(&logs, nil)), nil)
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

var stampNow = time.Unix(1_700_000_000, 0)

func stampAt(d time.Duration) int64 { return stampNow.Add(d).UnixNano() }

type stampedPost struct {
	stamp  string
	signed bool
	body   string
}

func postStamped(router http.Handler, key []byte, p stampedPost) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(p.body))
	req.Header.Set("Content-Type", jsonType)
	if p.stamp != "" {
		req.Header.Set(models.TimestampHeader, p.stamp)
	}
	if p.signed {
		req.Header.Set(sign.Header, sign.Sum(key, []byte(p.body)))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func stampOf(d time.Duration) string { return strconv.FormatInt(stampAt(d), 10) }

func TestRouter_Updates_StampReachesStorageOnlyWhenTrustedAndIsClampedWhenAhead(t *testing.T) {
	tests := []struct {
		name      string
		key       []byte
		post      stampedPost
		wantStamp int64
	}{
		{name: "no header is stored unstamped", post: stampedPost{body: goodListBody}, wantStamp: 0},
		{name: "server without a key takes an unsigned current stamp", post: stampedPost{stamp: stampOf(0), body: goodListBody}, wantStamp: stampAt(0)},
		{name: "server without a key takes a stamp 30 seconds ahead as is", post: stampedPost{stamp: stampOf(30 * time.Second), body: goodListBody}, wantStamp: stampAt(30 * time.Second)},
		{name: "stamp exactly one minute ahead is taken as is", post: stampedPost{stamp: stampOf(time.Minute), body: goodListBody}, wantStamp: stampAt(time.Minute)},
		{name: "server without a key clamps a stamp two minutes ahead to one minute", post: stampedPost{stamp: stampOf(2 * time.Minute), body: goodListBody}, wantStamp: stampAt(time.Minute)},
		{name: "max stamp is clamped to one minute ahead", post: stampedPost{stamp: strconv.FormatInt(math.MaxInt64, 10), body: goodListBody}, wantStamp: stampAt(time.Minute)},
		{name: "keyed server stores an unsigned max stamp unstamped", key: testKey, post: stampedPost{stamp: strconv.FormatInt(math.MaxInt64, 10), body: goodListBody}, wantStamp: 0},
		{name: "keyed server stores an unsigned current stamp unstamped", key: testKey, post: stampedPost{stamp: stampOf(0), body: goodListBody}, wantStamp: 0},
		{name: "keyed server takes a signed current stamp", key: testKey, post: stampedPost{stamp: stampOf(0), signed: true, body: goodListBody}, wantStamp: stampAt(0)},
		{name: "keyed server takes a signed stamp 30 seconds ahead as is", key: testKey, post: stampedPost{stamp: stampOf(30 * time.Second), signed: true, body: goodListBody}, wantStamp: stampAt(30 * time.Second)},
		{name: "keyed server clamps a signed stamp two minutes ahead", key: testKey, post: stampedPost{stamp: stampOf(2 * time.Minute), signed: true, body: goodListBody}, wantStamp: stampAt(time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}
			router := handler.NewRouterWithClock(s, nil, discardLogger, tt.key, func() time.Time { return stampNow })

			w := postStamped(router, tt.key, tt.post)

			assert.Equal(t, http.StatusOK, w.Code)
			require.Len(t, s.batches, 1)
			assert.Equal(t, metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 1},
				Counters: map[string]metrics.Counter{"hits": 1},
				Stamp:    tt.wantStamp,
			}, s.batches[0])
		})
	}
}

func TestRouter_Updates_MalformedStampIs400WithTheCauseAndStoresNothing(t *testing.T) {
	tests := []struct {
		name      string
		stamp     string
		wantCause string
	}{
		{name: "word", stamp: "soon", wantCause: `strconv.ParseInt: parsing "soon": invalid syntax`},
		{name: "float notation", stamp: "1.7e18", wantCause: `strconv.ParseInt: parsing "1.7e18": invalid syntax`},
		{name: "past int64", stamp: "9223372036854775808", wantCause: `strconv.ParseInt: parsing "9223372036854775808": value out of range`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}
			router := handler.NewRouter(s, nil, discardLogger, nil)

			w := postStamped(router, nil, stampedPost{stamp: tt.stamp, body: goodListBody})

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, fmt.Sprintf("invalid %s: want Unix nanoseconds: %s\n", models.TimestampHeader, tt.wantCause), w.Body.String())
			assert.Empty(t, s.batches)
		})
	}
}

func TestRouter_Updates_StampStateIsLoggedOnlyWhenItChanges(t *testing.T) {
	ahead := stampedPost{stamp: stampOf(5 * time.Minute), signed: true, body: goodListBody}
	current := stampedPost{stamp: stampOf(0), signed: true, body: goodListBody}
	unsigned := stampedPost{stamp: stampOf(0), body: goodListBody}
	tests := []struct {
		name  string
		posts []stampedPost
		want  []string
	}{
		{
			name:  "clamped batches log one Warn and the first one within the limit one Info",
			posts: []stampedPost{current, ahead, ahead, ahead, current, current},
			want: []string{
				fmt.Sprintf(`level=WARN msg="stamps clamped: ahead of the server clock" stamp=%d limit=%d`, stampAt(5*time.Minute), stampAt(time.Minute)),
				fmt.Sprintf(`level=INFO msg="stamps no longer clamped: within the server clock limit" stamp=%d`, stampAt(0)),
			},
		},
		{
			name:  "clamping that starts again logs again",
			posts: []stampedPost{ahead, current, ahead},
			want: []string{
				fmt.Sprintf(`level=WARN msg="stamps clamped: ahead of the server clock" stamp=%d limit=%d`, stampAt(5*time.Minute), stampAt(time.Minute)),
				fmt.Sprintf(`level=INFO msg="stamps no longer clamped: within the server clock limit" stamp=%d`, stampAt(0)),
				fmt.Sprintf(`level=WARN msg="stamps clamped: ahead of the server clock" stamp=%d limit=%d`, stampAt(5*time.Minute), stampAt(time.Minute)),
			},
		},
		{
			name:  "unsigned stamps log one Warn and the first signed one one Info",
			posts: []stampedPost{unsigned, unsigned, unsigned, current, current},
			want: []string{
				fmt.Sprintf(`level=WARN msg="stamps ignored: request is not signed" stamp=%d`, stampAt(0)),
				fmt.Sprintf(`level=INFO msg="stamps honoured again: request is signed" stamp=%d`, stampAt(0)),
			},
		},
		{
			name:  "trusted stamps within the limit log nothing",
			posts: []stampedPost{current, current, {body: goodListBody}},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			lg := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			}))
			router := handler.NewRouterWithClock(&fakeStorage{}, nil, lg, testKey, func() time.Time { return stampNow })

			for i, p := range tt.posts {
				assert.Equal(t, http.StatusOK, postStamped(router, testKey, p).Code, "post %d", i+1)
			}

			var got []string
			for line := range strings.Lines(logs.String()) {
				if strings.Contains(line, `msg="stamps `) {
					got = append(got, strings.TrimSuffix(line, "\n"))
				}
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// Through memstorage, since the order check now lives in the storage: a handler that dropped the stamp
// would let the older gauge win.
func TestRouter_Updates_OutOfOrderBatchesKeepTheNewestGauge(t *testing.T) {
	now := stampNow
	s := memstorage.New()
	router := handler.NewRouterWithClock(s, nil, discardLogger, testKey, func() time.Time { return now })
	post := func(stamp string, value int) {
		t.Helper()
		body := fmt.Sprintf(`[{"id":"temp","type":"gauge","value":%d},{"id":"hits","type":"counter","delta":1}]`, value)
		require.Equal(t, http.StatusOK, postStamped(router, testKey, stampedPost{stamp: stamp, signed: true, body: body}).Code)
	}
	gauge := func() metrics.Gauge {
		t.Helper()
		v, err := s.Gauge(t.Context(), "temp")
		require.NoError(t, err)
		return v
	}

	post(stampOf(0), 2)
	post(stampOf(-time.Second), 1)
	assert.Equal(t, metrics.Gauge(2), gauge(), "an older batch after a newer one")

	post(strconv.FormatInt(math.MaxInt64, 10), 3)
	post(stampOf(30*time.Second), 4)
	assert.Equal(t, metrics.Gauge(3), gauge(), "a far-future stamp holds the gauge while the clock is within a minute of it")

	now = now.Add(time.Minute + time.Second)
	post(stampOf(time.Minute+time.Second), 5)
	assert.Equal(t, metrics.Gauge(5), gauge(), "a minute later a current stamp passes the clamped one")

	hits, err := s.Counter(t.Context(), "hits")
	require.NoError(t, err)
	assert.Equal(t, metrics.Counter(5), hits, "counters are added whatever the stamp")
}
