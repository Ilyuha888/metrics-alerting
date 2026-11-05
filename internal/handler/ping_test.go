package handler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePinger struct {
	err          error
	ctx          context.Context
	ctxErrAtCall error
}

func (f *fakePinger) PingContext(ctx context.Context) error {
	f.ctx = ctx
	f.ctxErrAtCall = ctx.Err()
	return f.err
}

func getPing(db handler.Pinger, lg *slog.Logger) *httptest.ResponseRecorder {
	router := handler.NewRouter(&fakeStorage{}, db, lg)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	return w
}

func TestRouter_Ping(t *testing.T) {
	const cause = "dial postgres://user:secret@host failed"
	tests := []struct {
		name     string
		db       handler.Pinger
		wantCode int
		wantLog  string
	}{
		{name: "reachable database is 200", db: &fakePinger{}, wantCode: http.StatusOK},
		{
			name:     "failed ping is 500 and logs the cause",
			db:       &fakePinger{err: errors.New(cause)},
			wantCode: http.StatusInternalServerError,
			wantLog:  cause,
		},
		{name: "no database configured is 500", db: nil, wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			lg := slog.New(slog.NewTextHandler(&logs, nil))

			w := getPing(tt.db, lg)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.NotContains(t, w.Body.String(), "secret")
			assert.Contains(t, logs.String(), tt.wantLog)
		})
	}
}

func TestRouter_Ping_ContextHasDeadline(t *testing.T) {
	fake := &fakePinger{}

	getPing(fake, discardLogger)

	require.NotNil(t, fake.ctx)
	deadline, ok := fake.ctx.Deadline()
	require.True(t, ok)
	assert.LessOrEqual(t, time.Until(deadline), 3*time.Second)
}

func TestRouter_Ping_ContextComesFromRequest(t *testing.T) {
	fake := &fakePinger{}
	router := handler.NewRouter(&fakeStorage{}, fake, discardLogger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil).WithContext(ctx)

	router.ServeHTTP(httptest.NewRecorder(), req)

	require.NotNil(t, fake.ctx)
	assert.ErrorIs(t, fake.ctxErrAtCall, context.Canceled)
}
