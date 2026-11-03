package agent

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
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

			require.NoError(t, sender.Send(tt.snap))

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

			require.NoError(t, sender.Send(tt.snap))

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

			require.NoError(t, sender.Send(tt.snap))

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

			err := sender.Send(Snapshot{Gauges: map[string]metrics.Gauge{"a": 1, "b": 2}, PollCount: 1})

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

	require.Error(t, sender.Send(Snapshot{PollCount: 1}))
	require.Error(t, sender.Send(Snapshot{PollCount: 1}))

	assert.EqualValues(t, 1, connections.Load())
}

func TestSender_Send_UnreachableServerIsAnError(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusOK)
	sender.endpoint = "http://127.0.0.1:1" // nothing listens here

	err := sender.Send(Snapshot{PollCount: 1})

	require.Error(t, err)
	assert.Empty(t, rec.got)
}
