package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithLogging_ServeHTTP_LogsOneRecordPerRequest(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		inner      http.HandlerFunc
		wantURI    string
		wantStatus int
		wantSize   int
	}{
		{
			name:   "handler that only writes logs 200",
			method: http.MethodGet,
			target: "/value/counter/x",
			inner: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "42")
			},
			wantURI:    "/value/counter/x",
			wantStatus: http.StatusOK,
			wantSize:   2,
		},
		{
			name:   "size sums every write",
			method: http.MethodGet,
			target: "/value/gauge/g",
			inner: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "ab")
				_, _ = io.WriteString(w, "cde")
			},
			wantURI:    "/value/gauge/g",
			wantStatus: http.StatusOK,
			wantSize:   5,
		},
		{
			name:   "uri keeps the query string",
			method: http.MethodPost,
			target: "/update/gauge/g/1.5?source=agent",
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantURI:    "/update/gauge/g/1.5?source=agent",
			wantStatus: http.StatusOK,
			wantSize:   0,
		},
		{
			name:       "http.Error status and body are logged",
			method:     http.MethodGet,
			target:     "/value/gauge/missing",
			inner:      func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not found", http.StatusNotFound) },
			wantURI:    "/value/gauge/missing",
			wantStatus: http.StatusNotFound,
			wantSize:   len("not found\n"),
		},
		{
			name:   "explicit WriteHeader status is logged",
			method: http.MethodPost,
			target: "/created",
			inner: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, "ok")
			},
			wantURI:    "/created",
			wantStatus: http.StatusCreated,
			wantSize:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			lg := slog.New(slog.NewJSONHandler(&buf, nil))
			h := handler.WithLogging(lg, tt.inner)
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			require.Equal(t, tt.wantStatus, rec.Code, "the wrapper must not change what the client gets")
			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			require.Len(t, lines, 1)
			var entry map[string]any
			require.NoError(t, json.Unmarshal(lines[0], &entry))
			assert.Equal(t, "INFO", entry["level"])
			assert.Equal(t, "request", entry["msg"])
			assert.Equal(t, tt.wantURI, entry["uri"])
			assert.Equal(t, tt.method, entry["method"])
			assert.EqualValues(t, tt.wantStatus, entry["status"])
			assert.EqualValues(t, tt.wantSize, entry["size"])
			assert.Contains(t, entry, "duration")
		})
	}
}
