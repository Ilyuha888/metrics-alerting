package agent

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
// no guard: Send waits for each response before sending the next request.
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

func decodeBody(t *testing.T, body []byte) models.Metrics {
	t.Helper()

	var m models.Metrics
	require.NoError(t, json.Unmarshal(body, &m))
	return m
}

func float(v float64) *float64 { return &v }

func integer(v int64) *int64 { return &v }

func TestSender_Send_BuildsTheRequestTheSpecDescribes(t *testing.T) {
	tests := []struct {
		name string
		snap Snapshot
		want models.Metrics
	}{
		{
			name: "gauge keeps its fraction",
			snap: Snapshot{Gauges: map[string]metrics.Gauge{"temp": 36.6}},
			want: models.Metrics{ID: "temp", MType: metrics.KindGauge, Value: float(36.6)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, rec := recordingServer(t, http.StatusOK)

			require.NoError(t, sender.Send(tt.snap))

			got := rec.got
			require.Len(t, got, 2) // gauge + PollCount
			assert.Equal(t, "/update", got[0].path)
			assert.Equal(t, http.MethodPost, got[0].method)
			assert.Equal(t, "application/json", got[0].contentType)
			assert.Equal(t, "gzip", got[0].contentEncoding)
			assert.Equal(t, tt.want, decodeBody(t, got[0].body))
		})
	}
}

func TestSender_Send_AlwaysReportsPollCount(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusOK)

	require.NoError(t, sender.Send(Snapshot{PollCount: 5}))

	require.Len(t, rec.got, 1)
	assert.Equal(t, "/update", rec.got[0].path)
	want := models.Metrics{ID: "PollCount", MType: metrics.KindCounter, Delta: integer(5)}
	assert.Equal(t, want, decodeBody(t, rec.got[0].body))
}

func TestSender_Send_DeliversTheRestAfterAFailure(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusInternalServerError)
	snap := Snapshot{
		Gauges:    map[string]metrics.Gauge{"a": 1, "b": 2, "c": 3},
		PollCount: 1,
	}

	err := sender.Send(snap)

	require.Error(t, err)
	assert.Len(t, rec.got, 4, "every metric is attempted even though all of them fail")
	assert.Contains(t, err.Error(), "4 of 4 metrics failed")
}

func TestSender_Send_UnreachableServerIsAnError(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusOK)
	sender.endpoint = "http://127.0.0.1:1" // nothing listens here

	err := sender.Send(Snapshot{PollCount: 1})

	require.Error(t, err)
	assert.Empty(t, rec.got)
}
