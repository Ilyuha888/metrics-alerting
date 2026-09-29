package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorded struct {
	method      string
	path        string
	contentType string
}

// recorder answers every request with status and records what arrived. The slice needs
// no guard: Send waits for each response before sending the next request.
type recorder struct {
	status int
	got    []recorded
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec.got = append(rec.got, recorded{r.Method, r.URL.Path, r.Header.Get("Content-Type")})
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

func TestSender_Send_BuildsTheRequestTheSpecDescribes(t *testing.T) {
	tests := []struct {
		name     string
		snap     Snapshot
		wantPath string
	}{
		{
			name:     "gauge keeps its fraction",
			snap:     Snapshot{Gauges: map[string]metrics.Gauge{"temp": 36.6}},
			wantPath: "/update/gauge/temp/36.6",
		},
		{
			name:     "whole gauge has no trailing zeros",
			snap:     Snapshot{Gauges: map[string]metrics.Gauge{"Alloc": 1500000}},
			wantPath: "/update/gauge/Alloc/1500000",
		},
		{
			name:     "large gauge is not printed in exponent form",
			snap:     Snapshot{Gauges: map[string]metrics.Gauge{"Sys": 1.2345678e+09}},
			wantPath: "/update/gauge/Sys/1234567800",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sender, rec := recordingServer(t, http.StatusOK)

			require.NoError(t, sender.Send(tt.snap))

			got := rec.got
			require.Len(t, got, 2) // gauge + PollCount
			assert.Equal(t, tt.wantPath, got[0].path)
			assert.Equal(t, http.MethodPost, got[0].method)
			assert.Equal(t, "text/plain", got[0].contentType)
		})
	}
}

func TestSender_Send_AlwaysReportsPollCount(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusOK)

	require.NoError(t, sender.Send(Snapshot{PollCount: 5}))

	require.Len(t, rec.got, 1)
	assert.Equal(t, "/update/counter/PollCount/5", rec.got[0].path)
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
