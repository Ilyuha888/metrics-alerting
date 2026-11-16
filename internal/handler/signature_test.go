package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/sign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testKey = []byte("secret")

const signedBatch = `[{"id":"temp","type":"gauge","value":36.6},{"id":"hits","type":"counter","delta":3}]`

func TestRouter_Signature_NoKeyAcceptsUnsignedPost(t *testing.T) {
	fake := &fakeStorage{}
	router := handler.NewRouter(fake, nil, discardLogger, nil)
	req := httptest.NewRequest(http.MethodPost, "/updates/", bytes.NewReader(gzipString(t, signedBatch)))
	req.Header.Set("Content-Type", jsonType)
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, fake.batches, 1)
	assert.Empty(t, res.Header.Get(sign.Header))
}

func TestRouter_Signature_PostWithAWrongSignatureIsRejected(t *testing.T) {
	gzipBody := gzipString(t, signedBatch)
	tests := []struct {
		name        string
		target      string
		contentType string
		gzip        bool
		body        []byte
		signature   string
		wantStatus  int
		wantAnswer  string
	}{
		{
			name:   "signature over the gzip body stores the batch",
			target: "/updates/", contentType: jsonType, gzip: true, body: gzipBody,
			signature:  sign.Sum(testKey, gzipBody),
			wantStatus: http.StatusOK,
		},
		{
			name:   "signature over the decompressed JSON is rejected",
			target: "/updates/", contentType: jsonType, gzip: true, body: gzipBody,
			signature:  sign.Sum(testKey, []byte(signedBatch)),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:   "signature under another key is rejected",
			target: "/updates/", contentType: jsonType, gzip: true, body: gzipBody,
			signature:  sign.Sum([]byte("other"), gzipBody),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:   "missing signature on a batch stores it",
			target: "/updates/", contentType: jsonType, gzip: true, body: gzipBody,
			wantStatus: http.StatusOK,
		},
		{
			name:   "missing signature on a JSON read answers it",
			target: "/value", contentType: jsonType, body: []byte(`{"id":"temp","type":"gauge"}`),
			wantStatus: http.StatusOK, wantAnswer: `{"id":"temp","type":"gauge","value":1}`,
		},
		{
			name:       "missing signature on a URL update stores it",
			target:     "/update/gauge/temp/1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "signature over the empty body of a URL update stores it",
			target:     "/update/gauge/temp/1",
			signature:  sign.Sum(testKey, nil),
			wantStatus: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{gauges: map[string]metrics.Gauge{"temp": 1}}
			router := handler.NewRouter(fake, nil, discardLogger, testKey)
			req := httptest.NewRequest(http.MethodPost, tt.target, bytes.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			if tt.gzip {
				req.Header.Set("Content-Encoding", "gzip")
			}
			if tt.signature != "" {
				req.Header.Set(sign.Header, tt.signature)
			}
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			res := rec.Result()
			defer res.Body.Close()
			assert.Equal(t, tt.wantStatus, res.StatusCode)
			switch {
			case tt.wantStatus != http.StatusOK:
				assert.Empty(t, fake.ctxErrs, "a rejected request must not reach storage")
			case tt.wantAnswer != "":
				assert.JSONEq(t, tt.wantAnswer, string(readBody(t, res)))
			case tt.gzip:
				require.Len(t, fake.batches, 1)
				assert.Equal(t, metrics.Batch{
					Gauges:   map[string]metrics.Gauge{"temp": 36.6},
					Counters: map[string]metrics.Counter{"hits": 3},
				}, fake.batches[0])
			default:
				assert.Equal(t, []call{{kind: metrics.KindGauge, name: "temp", gauge: 1}}, fake.calls)
			}
		})
	}
}

func TestRouter_Signature_AnswerIsSignedAsSent(t *testing.T) {
	valueRead := []byte(`{"id":"temp","type":"gauge"}`)
	tests := []struct {
		name          string
		method        string
		target        string
		body          []byte
		acceptGzip    bool
		unsigned      bool
		foreignKey    bool
		db            handler.Pinger
		wantStatus    int
		wantGzip      bool
		wantPlainBody string
		wantEmptyBody bool
	}{
		{
			name: "GET / passes unsigned", method: http.MethodGet, target: "/",
			wantStatus: http.StatusOK,
		},
		{
			name: "GET /value passes unsigned", method: http.MethodGet, target: "/value/gauge/temp",
			wantStatus: http.StatusOK, wantPlainBody: "36.6",
		},
		{
			name: "GET with a wrong signature passes", method: http.MethodGet, target: "/value/gauge/temp",
			foreignKey: true,
			wantStatus: http.StatusOK, wantPlainBody: "36.6",
		},
		{
			name: "GET /ping passes unsigned", method: http.MethodGet, target: "/ping", db: &fakePinger{},
			wantStatus: http.StatusOK, wantEmptyBody: true,
		},
		{
			name: "gzip answer is signed over the compressed bytes", method: http.MethodPost, target: "/value",
			body: valueRead, acceptGzip: true,
			wantStatus: http.StatusOK, wantGzip: true,
		},
		{
			name: "plain JSON answer", method: http.MethodPost, target: "/value", body: valueRead,
			wantStatus: http.StatusOK, wantPlainBody: `{"id":"temp","type":"gauge","value":36.6}`,
		},
		{
			name: "answer to an unsigned POST", method: http.MethodPost, target: "/value", body: valueRead,
			unsigned:   true,
			wantStatus: http.StatusOK, wantPlainBody: `{"id":"temp","type":"gauge","value":36.6}`,
		},
		{
			name: "404 answer", method: http.MethodGet, target: "/value/gauge/missing",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "signature rejection", method: http.MethodPost, target: "/value", body: valueRead,
			foreignKey: true,
			wantStatus: http.StatusBadRequest, wantPlainBody: "wrong HashSHA256\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStorage{gauges: map[string]metrics.Gauge{"temp": 36.6}}
			router := handler.NewRouter(fake, tt.db, discardLogger, testKey)
			req := httptest.NewRequest(tt.method, tt.target, bytes.NewReader(tt.body))
			if tt.body != nil {
				req.Header.Set("Content-Type", jsonType)
			}
			switch {
			case tt.foreignKey:
				req.Header.Set(sign.Header, sign.Sum([]byte("other"), tt.body))
			case tt.body != nil && !tt.unsigned:
				req.Header.Set(sign.Header, sign.Sum(testKey, tt.body))
			}
			if tt.acceptGzip {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			res := rec.Result()
			defer res.Body.Close()
			raw := readBody(t, res)
			require.Equal(t, tt.wantStatus, res.StatusCode)
			require.Equal(t, tt.wantEmptyBody, len(raw) == 0)
			assert.Equal(t, sign.Sum(testKey, raw), res.Header.Get(sign.Header))
			if tt.wantGzip {
				require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
				plain := gunzipString(t, raw)
				assert.JSONEq(t, `{"id":"temp","type":"gauge","value":36.6}`, plain)
				assert.NotEqual(t, sign.Sum(testKey, []byte(plain)), res.Header.Get(sign.Header))
				return
			}
			assert.Empty(t, res.Header.Get("Content-Encoding"))
			if tt.wantPlainBody != "" {
				assert.Equal(t, tt.wantPlainBody, string(raw))
			}
		})
	}
}
