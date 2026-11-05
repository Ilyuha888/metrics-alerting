package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/Ilyuha888/metrics-alerting/internal/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorded struct {
	method          string
	path            string
	contentType     string
	contentEncoding string
	body            []byte
}

// recorder answers every request with status and records what arrived. The slice needs
// no guard: Send waits for the response before it returns.
type recorder struct {
	status int
	got    []recorded
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A body that is not gzip stays empty, so decodeBody fails the test that reads it.
	var body []byte
	if zr, err := gzip.NewReader(r.Body); err == nil {
		body, _ = io.ReadAll(zr)
	}
	rec.got = append(rec.got, recorded{
		r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding"), body,
	})
	w.WriteHeader(rec.status)
}

// recordingServer starts a recorder behind a real HTTP server and points a Sender at it.
func recordingServer(t *testing.T, status int) (*Sender, *recorder) {
	t.Helper()

	rec := &recorder{status: status}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	return NewSender(srv.URL), rec
}

func decodeBody(t *testing.T, body []byte) []models.Metrics {
	t.Helper()

	var list []models.Metrics
	require.NoError(t, json.Unmarshal(body, &list))
	return list
}

func float(v float64) *float64 { return &v }

func integer(v int64) *int64 { return &v }

func TestSender_Send_BuildsOneRequestWithTheWholeSnapshot(t *testing.T) {
	tests := []struct {
		name string
		snap Snapshot
		want []models.Metrics
	}{
		{
			name: "gauge keeps its fraction",
			snap: Snapshot{Gauges: map[string]metrics.Gauge{"temp": 36.6}, PollCount: 3},
			want: []models.Metrics{
				{ID: "temp", MType: metrics.KindGauge, Value: float(36.6)},
				{ID: "PollCount", MType: metrics.KindCounter, Delta: integer(3)},
			},
		},
		{
			name: "every gauge is in the one list",
			snap: Snapshot{Gauges: map[string]metrics.Gauge{"a": 1, "b": 2, "c": 3}, PollCount: 1},
			want: []models.Metrics{
				{ID: "a", MType: metrics.KindGauge, Value: float(1)},
				{ID: "b", MType: metrics.KindGauge, Value: float(2)},
				{ID: "c", MType: metrics.KindGauge, Value: float(3)},
				{ID: "PollCount", MType: metrics.KindCounter, Delta: integer(1)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, rec := recordingServer(t, http.StatusOK)

			require.NoError(t, sender.Send(t.Context(), tt.snap))

			require.Len(t, rec.got, 1)
			assert.Equal(t, "/updates/", rec.got[0].path)
			assert.Equal(t, http.MethodPost, rec.got[0].method)
			assert.Equal(t, "application/json", rec.got[0].contentType)
			assert.Equal(t, "gzip", rec.got[0].contentEncoding)
			assert.ElementsMatch(t, tt.want, decodeBody(t, rec.got[0].body))
		})
	}
}

func TestSender_Send_AlwaysReportsPollCount(t *testing.T) {
	tests := []struct {
		name string
		snap Snapshot
		want []models.Metrics
	}{
		{
			name: "no gauges yet",
			snap: Snapshot{PollCount: 5},
			want: []models.Metrics{{ID: "PollCount", MType: metrics.KindCounter, Delta: integer(5)}},
		},
		{
			name: "zero polls next to a gauge",
			snap: Snapshot{Gauges: map[string]metrics.Gauge{"temp": 1}},
			want: []models.Metrics{
				{ID: "temp", MType: metrics.KindGauge, Value: float(1)},
				{ID: "PollCount", MType: metrics.KindCounter, Delta: integer(0)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, rec := recordingServer(t, http.StatusOK)

			require.NoError(t, sender.Send(t.Context(), tt.snap))

			require.Len(t, rec.got, 1)
			assert.ElementsMatch(t, tt.want, decodeBody(t, rec.got[0].body))
		})
	}
}

func TestSender_Send_EmptySnapshotSendsNoRequest(t *testing.T) {
	tests := []struct {
		name string
		snap Snapshot
	}{
		{name: "nil gauges", snap: Snapshot{}},
		{name: "empty gauges", snap: Snapshot{Gauges: map[string]metrics.Gauge{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, rec := recordingServer(t, http.StatusOK)

			require.NoError(t, sender.Send(t.Context(), tt.snap))

			assert.Empty(t, rec.got)
		})
	}
}

func TestSender_Send_Non200IsAnErrorAfterOneRequest(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "created is still not 200", status: http.StatusCreated},
		{name: "bad request", status: http.StatusBadRequest},
		{name: "too large", status: http.StatusRequestEntityTooLarge},
		{name: "server failure", status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, rec := recordingServer(t, tt.status)

			err := sender.Send(t.Context(), Snapshot{Gauges: map[string]metrics.Gauge{"a": 1, "b": 2}, PollCount: 1})

			require.Error(t, err)
			assert.Contains(t, err.Error(), strconv.Itoa(tt.status))
			require.Len(t, rec.got, 1)
			assert.Equal(t, "/updates/", rec.got[0].path)
		})
	}
}

func TestSender_Send_LargeErrorAnswerKeepsTheConnection(t *testing.T) {
	const moreThanCloseReads = 1 << 20
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, strings.Repeat("x", moreThanCloseReads), http.StatusInternalServerError)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	sender := NewSender(srv.URL)

	require.Error(t, sender.Send(t.Context(), Snapshot{PollCount: 1}))
	require.Error(t, sender.Send(t.Context(), Snapshot{PollCount: 1}))

	assert.EqualValues(t, 1, connections.Load())
}

func TestSender_Send_UnreachableServerIsAnError(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusOK)
	sender.endpoint = "http://127.0.0.1:1" // nothing listens here
	sender.SetDelays([]time.Duration{time.Millisecond})

	err := sender.Send(t.Context(), Snapshot{PollCount: 1})

	require.Error(t, err)
	assert.Empty(t, rec.got)
}

func closedPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// countDials counts every dial the sender makes. It dials for real, so a closed port still refuses.
func countDials(sender *Sender) *atomic.Int32 {
	dials := new(atomic.Int32)
	dialer := &net.Dialer{}
	sender.client.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.Add(1)
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return dials
}

// serveAfter starts h on addr after d. The first dial has d to be refused, far more than a refusal
// on loopback takes.
func serveAfter(t *testing.T, addr string, d time.Duration, h http.Handler) {
	t.Helper()
	started := make(chan *httptest.Server, 1)
	time.AfterFunc(d, func() {
		srv := httptest.NewUnstartedServer(h)
		srv.Listener.Close()
		l, err := net.Listen("tcp", addr)
		if !assert.NoError(t, err) {
			started <- nil
			return
		}
		srv.Listener = l
		srv.Start()
		started <- srv
	})
	t.Cleanup(func() {
		if srv := <-started; srv != nil {
			srv.Close()
		}
	})
}

func TestSender_Send_ServerComingUpDuringAPauseGetsTheWholeBody(t *testing.T) {
	const opensAfter, pause = 200 * time.Millisecond, 600 * time.Millisecond
	addr := closedPort(t)
	sender := NewSender("http://" + addr)
	sender.SetDelays([]time.Duration{pause})
	dials := countDials(sender)
	rec := &recorder{status: http.StatusOK}
	serveAfter(t, addr, opensAfter, rec)

	err := sender.Send(t.Context(), Snapshot{Gauges: map[string]metrics.Gauge{"a": 1.5, "b": 2.5}, PollCount: 3})

	require.NoError(t, err)
	assert.EqualValues(t, 2, dials.Load())
	require.Len(t, rec.got, 1)
	assert.Equal(t, "gzip", rec.got[0].contentEncoding)
	assert.ElementsMatch(t, []models.Metrics{
		{ID: "a", MType: metrics.KindGauge, Value: float(1.5)},
		{ID: "b", MType: metrics.KindGauge, Value: float(2.5)},
		{ID: "PollCount", MType: metrics.KindCounter, Delta: integer(3)},
	}, decodeBody(t, rec.got[0].body))
}

func TestSender_Send_ClosedPortDialsFourTimes(t *testing.T) {
	sender := NewSender("http://" + closedPort(t))
	sender.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	dials := countDials(sender)

	err := sender.Send(t.Context(), Snapshot{PollCount: 1})

	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
	assert.Equal(t, "dial", opErr.Op)
	assert.ErrorContains(t, err, "after 4 attempts")
	assert.EqualValues(t, 4, dials.Load())
}

func TestNewSender_RepeatsWithTheDefaultDelays(t *testing.T) {
	assert.Equal(t, retry.DefaultDelays, NewSender("http://localhost").delays)
}

func TestSender_Send_FailureAfterTheRequestWentOutIsNotRepeated(t *testing.T) {
	tests := []struct {
		name      string
		handler   func(w http.ResponseWriter, r *http.Request)
		timeout   time.Duration
		wantOpErr bool
	}{
		{
			name: "server closes the connection before answering",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if assert.NoError(t, err) {
					conn.Close()
				}
			},
		},
		{
			name: "server resets the connection before answering",
			handler: func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				conn, _, err := w.(http.Hijacker).Hijack()
				if assert.NoError(t, err) {
					assert.NoError(t, conn.(*net.TCPConn).SetLinger(0))
					conn.Close()
				}
			},
			wantOpErr: true,
		},
		{
			name: "no answer within the client's timeout",
			handler: func(_ http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
			},
			timeout: 100 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				tt.handler(w, r)
			}))
			defer srv.Close()
			sender := NewSender(srv.URL)
			sender.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
			if tt.timeout > 0 {
				sender.client.Timeout = tt.timeout
			}

			err := sender.Send(t.Context(), Snapshot{PollCount: 1})

			require.Error(t, err)
			var opErr *net.OpError
			isOpErr := errors.As(err, &opErr)
			assert.False(t, isOpErr && opErr.Op == "dial", "%v", err)
			if tt.wantOpErr {
				assert.True(t, isOpErr, "%v", err)
			}
			assert.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestSender_Send_CancelDuringAPauseReturnsAtOnce(t *testing.T) {
	sender := NewSender("http://" + closedPort(t))
	sender.SetDelays([]time.Duration{time.Hour, time.Hour})
	dials := countDials(sender)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)

	go func() { result <- sender.Send(ctx, Snapshot{PollCount: 1}) }()
	require.Eventually(t, func() bool { return dials.Load() == 1 }, 2*time.Second, time.Millisecond)
	cancel()

	select {
	case err := <-result:
		assert.ErrorIs(t, err, context.Canceled)
		var opErr *net.OpError
		require.ErrorAs(t, err, &opErr)
		assert.Equal(t, "dial", opErr.Op)
	case <-time.After(2 * time.Second):
		require.Fail(t, "Send did not return on the cancel")
	}
	assert.EqualValues(t, 1, dials.Load())
}

func TestSender_Send_CancelWhileTheServerHoldsItsAnswerWaitsForIt(t *testing.T) {
	const longerThanACutTakes = 200 * time.Millisecond
	var requests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	sender := NewSender(srv.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)

	go func() { result <- sender.Send(ctx, Snapshot{PollCount: 1}) }()
	<-entered
	cancel()

	select {
	case err := <-result:
		require.Fail(t, "Send returned before the answer", "%v", err)
	case <-time.After(longerThanACutTakes):
	}
	releaseOnce()

	assert.NoError(t, <-result)
	assert.EqualValues(t, 1, requests.Load())
}
