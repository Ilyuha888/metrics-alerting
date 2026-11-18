package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/handler"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postStamped(router http.Handler, stamp, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(body))
	req.Header.Set("Content-Type", jsonType)
	if stamp != "" {
		req.Header.Set(models.TimestampHeader, stamp)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func batch(gauges map[string]metrics.Gauge, counters map[string]metrics.Counter) metrics.Batch {
	if gauges == nil {
		gauges = map[string]metrics.Gauge{}
	}
	if counters == nil {
		counters = map[string]metrics.Counter{}
	}
	return metrics.Batch{Gauges: gauges, Counters: counters}
}

func TestRouter_Updates_StampedBatchKeepsTheNewestGauge(t *testing.T) {
	type post struct {
		stamp      string
		body       string
		writeErr   error
		wantStatus int
	}
	tests := []struct {
		name  string
		posts []post
		want  []metrics.Batch
	}{
		{
			name: "older batch after a newer one keeps the newer gauge and adds the counter",
			posts: []post{
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":2},{"id":"hits","type":"counter","delta":1}]`, wantStatus: http.StatusOK},
				{stamp: "100", body: `[{"id":"temp","type":"gauge","value":1},{"id":"fresh","type":"gauge","value":5},{"id":"hits","type":"counter","delta":1}]`, wantStatus: http.StatusOK},
			},
			want: []metrics.Batch{
				batch(map[string]metrics.Gauge{"temp": 2}, map[string]metrics.Counter{"hits": 1}),
				batch(map[string]metrics.Gauge{"fresh": 5}, map[string]metrics.Counter{"hits": 1}),
			},
		},
		{
			name: "equal stamp skips the gauge",
			posts: []post{
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":2}]`, wantStatus: http.StatusOK},
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":3},{"id":"hits","type":"counter","delta":1}]`, wantStatus: http.StatusOK},
			},
			want: []metrics.Batch{
				batch(map[string]metrics.Gauge{"temp": 2}, nil),
				batch(nil, map[string]metrics.Counter{"hits": 1}),
			},
		},
		{
			name: "newer batch replaces the gauge",
			posts: []post{
				{stamp: "100", body: `[{"id":"temp","type":"gauge","value":1}]`, wantStatus: http.StatusOK},
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":2}]`, wantStatus: http.StatusOK},
			},
			want: []metrics.Batch{
				batch(map[string]metrics.Gauge{"temp": 1}, nil),
				batch(map[string]metrics.Gauge{"temp": 2}, nil),
			},
		},
		{
			name: "older batch of stale gauges only writes nothing and answers 200",
			posts: []post{
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":2}]`, wantStatus: http.StatusOK},
				{stamp: "100", body: `[{"id":"temp","type":"gauge","value":1}]`, wantStatus: http.StatusOK},
			},
			want: []metrics.Batch{
				batch(map[string]metrics.Gauge{"temp": 2}, nil),
			},
		},
		{
			name: "batch without the header is stored as is after a stamped one",
			posts: []post{
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":2}]`, wantStatus: http.StatusOK},
				{body: `[{"id":"temp","type":"gauge","value":1}]`, wantStatus: http.StatusOK},
			},
			want: []metrics.Batch{
				batch(map[string]metrics.Gauge{"temp": 2}, nil),
				batch(map[string]metrics.Gauge{"temp": 1}, nil),
			},
		},
		{
			name: "failed write records no stamp",
			posts: []post{
				{stamp: "200", body: `[{"id":"temp","type":"gauge","value":2}]`, writeErr: errors.New("disk full"), wantStatus: http.StatusInternalServerError},
				{stamp: "100", body: `[{"id":"temp","type":"gauge","value":1}]`, wantStatus: http.StatusOK},
			},
			want: []metrics.Batch{
				batch(map[string]metrics.Gauge{"temp": 2}, nil),
				batch(map[string]metrics.Gauge{"temp": 1}, nil),
			},
		},
		{
			name: "malformed stamp is rejected and stores nothing",
			posts: []post{
				{stamp: "soon", body: `[{"id":"temp","type":"gauge","value":1}]`, wantStatus: http.StatusBadRequest},
				{stamp: "1.7e18", body: `[{"id":"temp","type":"gauge","value":1}]`, wantStatus: http.StatusBadRequest},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStorage{}
			router := handler.NewRouter(s, nil, discardLogger, nil)

			for i, p := range tt.posts {
				s.writeErr = p.writeErr
				w := postStamped(router, p.stamp, p.body)
				assert.Equal(t, p.wantStatus, w.Code, "post %d", i+1)
			}

			assert.Equal(t, tt.want, s.batches)
		})
	}
}

// gatedStorage holds every batch write until release closes, and reports each one on entered.
type gatedStorage struct {
	*fakeStorage
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
}

func (g *gatedStorage) UpdateBatch(ctx context.Context, b metrics.Batch) error {
	g.entered <- struct{}{}
	<-g.release
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fakeStorage.UpdateBatch(ctx, b)
}

func TestRouter_Updates_ConcurrentStampedBatchesDoNotCross(t *testing.T) {
	s := &gatedStorage{fakeStorage: &fakeStorage{}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	router := handler.NewRouter(s, nil, discardLogger, nil)
	codes := make([]int, 2)
	var wg sync.WaitGroup

	wg.Go(func() {
		codes[0] = postStamped(router, "200", `[{"id":"temp","type":"gauge","value":2}]`).Code
	})
	<-s.entered
	wg.Go(func() {
		codes[1] = postStamped(router, "100", `[{"id":"temp","type":"gauge","value":1},{"id":"hits","type":"counter","delta":1}]`).Code
	})
	select {
	case <-s.entered:
		assert.Fail(t, "the older batch reached storage while the newer one was being written")
	case <-time.After(100 * time.Millisecond):
	}
	close(s.release)
	wg.Wait()

	assert.Equal(t, []int{http.StatusOK, http.StatusOK}, codes)
	require.Len(t, s.batches, 2)
	assert.Equal(t, batch(map[string]metrics.Gauge{"temp": 2}, nil), s.batches[0])
	assert.Equal(t, batch(nil, map[string]metrics.Counter{"hits": 1}), s.batches[1])
}
