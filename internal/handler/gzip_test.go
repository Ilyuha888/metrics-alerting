package handler_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipString(t *testing.T, s string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := io.WriteString(zw, s)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// readBody reads the response from rec.Result(), never from rec.Header(): the recorder's live
// map also shows headers set after WriteHeader, which never reach the client.
func readBody(t *testing.T, res *http.Response) []byte {
	t.Helper()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return body
}

func gunzipString(t *testing.T, b []byte) string {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(out)
}

func TestWithGzip_ServeHTTP_CompressesOnlyJSONAndHTMLForGzipClients(t *testing.T) {
	const jsonBody = `{"id":"temp","type":"gauge","value":36.6}`
	tests := []struct {
		name           string
		acceptEncoding []string
		inner          http.HandlerFunc
		wantStatus     int
		wantGzip       bool
		wantBody       string
	}{
		{
			name:           "json is compressed and loses the handler's Content-Length",
			acceptEncoding: []string{"gzip"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", strconv.Itoa(len(jsonBody)))
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, jsonBody)
			},
			wantStatus: http.StatusOK,
			wantGzip:   true,
			wantBody:   jsonBody,
		},
		{
			name:           "html with charset is compressed",
			acceptEncoding: []string{"gzip"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, "<p>hi</p>")
			},
			wantStatus: http.StatusOK,
			wantGzip:   true,
			wantBody:   "<p>hi</p>",
		},
		{
			name:           "handler that only writes is compressed",
			acceptEncoding: []string{"gzip"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"a":`)
				_, _ = io.WriteString(w, `1}`)
			},
			wantStatus: http.StatusOK,
			wantGzip:   true,
			wantBody:   `{"a":1}`,
		},
		{
			name:           "status from WriteHeader before the body survives compression",
			acceptEncoding: []string{"gzip"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, jsonBody)
			},
			wantStatus: http.StatusCreated,
			wantGzip:   true,
			wantBody:   jsonBody,
		},
		{
			name:           "gzip in the second Accept-Encoding line, any case, with q",
			acceptEncoding: []string{"br", "deflate, GZIP;q=0.5"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, jsonBody)
			},
			wantStatus: http.StatusOK,
			wantGzip:   true,
			wantBody:   jsonBody,
		},
		{
			name:           "text/plain stays plain",
			acceptEncoding: []string{"gzip"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = io.WriteString(w, "36.6")
			},
			wantStatus: http.StatusOK,
			wantBody:   "36.6",
		},
		{
			name:           "http.Error stays plain",
			acceptEncoding: []string{"gzip"},
			inner: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "bad request", http.StatusBadRequest)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   "bad request\n",
		},
		{
			name: "json without Accept-Encoding stays plain",
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, jsonBody)
			},
			wantStatus: http.StatusOK,
			wantBody:   jsonBody,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, v := range tt.acceptEncoding {
				req.Header.Add("Accept-Encoding", v)
			}
			rec := httptest.NewRecorder()

			handler.WithGzip(tt.inner).ServeHTTP(rec, req)

			res := rec.Result()
			defer res.Body.Close()
			body := readBody(t, res)
			assert.Equal(t, tt.wantStatus, res.StatusCode)
			if !tt.wantGzip {
				assert.Empty(t, res.Header.Get("Content-Encoding"))
				assert.Equal(t, tt.wantBody, string(body))
				return
			}
			assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
			assert.Empty(t, res.Header.Get("Content-Length"))
			assert.Equal(t, tt.wantBody, gunzipString(t, body))
		})
	}
}

func TestWithGzip_ServeHTTP_AcceptsOnlyGzipAndIdentityBodies(t *testing.T) {
	tests := []struct {
		name               string
		contentEncoding    string
		body               []byte
		wantStatus         int
		wantBody           string
		wantAcceptEncoding string
	}{
		{
			name:            "gzip body reaches the handler decoded",
			contentEncoding: "gzip",
			body:            gzipString(t, "payload"),
			wantStatus:      http.StatusOK,
			wantBody:        "payload",
		},
		{
			name:            "identity body passes as is",
			contentEncoding: "identity",
			body:            []byte("payload"),
			wantStatus:      http.StatusOK,
			wantBody:        "payload",
		},
		{
			name:       "body without Content-Encoding passes as is",
			body:       []byte("payload"),
			wantStatus: http.StatusOK,
			wantBody:   "payload",
		},
		{
			name:            "broken gzip is 400",
			contentEncoding: "gzip",
			body:            []byte("payload"),
			wantStatus:      http.StatusBadRequest,
			wantBody:        "body is not valid gzip\n",
		},
		{
			name:               "br is 415 and the answer names gzip",
			contentEncoding:    "br",
			body:               []byte("payload"),
			wantStatus:         http.StatusUnsupportedMediaType,
			wantBody:           "unsupported Content-Encoding: br\n",
			wantAcceptEncoding: "gzip",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				_, _ = w.Write(body)
			})
			req := httptest.NewRequest(http.MethodPost, "/update", bytes.NewReader(tt.body))
			if tt.contentEncoding != "" {
				req.Header.Set("Content-Encoding", tt.contentEncoding)
			}
			rec := httptest.NewRecorder()

			handler.WithGzip(echo).ServeHTTP(rec, req)

			res := rec.Result()
			defer res.Body.Close()
			body := readBody(t, res)
			assert.Equal(t, tt.wantStatus, res.StatusCode)
			assert.Equal(t, tt.wantBody, string(body))
			assert.Equal(t, tt.wantAcceptEncoding, res.Header.Get("Accept-Encoding"))
		})
	}
}

func TestRouter_Gzip_LogShowsCompressedSize(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, nil))
	router := handler.NewRouter(&fakeStorage{gauges: map[string]metrics.Gauge{"temp": 36.6}}, lg)
	req := httptest.NewRequest(http.MethodPost, "/value", strings.NewReader(`{"id":"temp","type":"gauge"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	body := readBody(t, res)
	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	plain := gunzipString(t, body)
	require.NotEqual(t, len(plain), len(body), "sizes must differ for the check to mean anything")
	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.EqualValues(t, len(body), entry["size"], "the log counts the bytes on the wire")
}

func TestRouter_Gzip_RejectionIsLogged(t *testing.T) {
	tests := []struct {
		name            string
		contentEncoding string
		wantStatus      int
		wantBody        string
	}{
		{
			name:            "broken gzip logs 400",
			contentEncoding: "gzip",
			wantStatus:      http.StatusBadRequest,
			wantBody:        "body is not valid gzip\n",
		},
		{
			name:            "br logs 415",
			contentEncoding: "br",
			wantStatus:      http.StatusUnsupportedMediaType,
			wantBody:        "unsupported Content-Encoding: br\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&buf, nil))
			fake := &fakeStorage{}
			router := handler.NewRouter(fake, lg)
			req := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader("payload"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Content-Encoding", tt.contentEncoding)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			res := rec.Result()
			defer res.Body.Close()
			body := readBody(t, res)
			require.Equal(t, tt.wantStatus, res.StatusCode)
			assert.Equal(t, tt.wantBody, string(body))
			assert.Empty(t, fake.calls, "a rejected request must not reach storage")
			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			require.Len(t, lines, 1)
			var entry map[string]any
			require.NoError(t, json.Unmarshal(lines[0], &entry))
			assert.Equal(t, "/update", entry["uri"])
			assert.EqualValues(t, tt.wantStatus, entry["status"])
			assert.EqualValues(t, len(tt.wantBody), entry["size"])
		})
	}
}

func TestRouter_Gzip_CompressesJSONButNotPlainText(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		target          string
		body            []byte
		wantContentType string
		wantGzip        bool
		wantBody        string
	}{
		{
			name:            "gzip JSON update answers gzip JSON",
			method:          http.MethodPost,
			target:          "/update",
			body:            gzipString(t, `{"id":"temp","type":"gauge","value":36.6}`),
			wantContentType: "application/json",
			wantGzip:        true,
			wantBody:        `{"id":"temp","type":"gauge","value":36.6}`,
		},
		{
			name:            "URL value read stays plain text",
			method:          http.MethodGet,
			target:          "/value/gauge/temp",
			wantContentType: "text/plain; charset=utf-8",
			wantBody:        "36.6",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := handler.NewRouter(&fakeStorage{gauges: map[string]metrics.Gauge{"temp": 36.6}}, discardLogger)
			req := httptest.NewRequest(tt.method, tt.target, bytes.NewReader(tt.body))
			if tt.body != nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Content-Encoding", "gzip")
			}
			req.Header.Set("Accept-Encoding", "gzip")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			res := rec.Result()
			defer res.Body.Close()
			body := readBody(t, res)
			require.Equal(t, http.StatusOK, res.StatusCode)
			assert.Equal(t, tt.wantContentType, res.Header.Get("Content-Type"))
			if !tt.wantGzip {
				assert.Empty(t, res.Header.Get("Content-Encoding"))
				assert.Equal(t, tt.wantBody, string(body))
				return
			}
			assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
			assert.JSONEq(t, tt.wantBody, gunzipString(t, body))
		})
	}
}
