package agent

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReporter struct {
	sent []Snapshot
	err  error
}

func (s *stubReporter) Send(snap Snapshot) error {
	s.sent = append(s.sent, snap)
	return s.err
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// step is driven directly instead of Run, which sleeps and never returns.
func TestAgent_step_ReportsEveryNthPoll(t *testing.T) {
	tests := []struct {
		name          string
		poll, report  time.Duration
		ticks         int
		wantReports   int
		wantLastDelta int64
	}{
		{name: "nothing to report before the interval", poll: 2 * time.Second, report: 10 * time.Second, ticks: 4, wantReports: 0},
		{name: "one report after five polls", poll: 2 * time.Second, report: 10 * time.Second, ticks: 5, wantReports: 1, wantLastDelta: 5},
		{name: "second report carries only the new polls", poll: 2 * time.Second, report: 10 * time.Second, ticks: 10, wantReports: 2, wantLastDelta: 5},
		{name: "equal intervals report every poll", poll: time.Second, report: time.Second, ticks: 3, wantReports: 3, wantLastDelta: 1},
		{name: "report shorter than poll still reports", poll: 10 * time.Second, report: time.Second, ticks: 2, wantReports: 2, wantLastDelta: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := &stubReporter{}
			a := New(NewCollector(), rep, tt.poll, tt.report, discard)

			for i := 1; i <= tt.ticks; i++ {
				a.step(i)
			}

			require.Len(t, rep.sent, tt.wantReports)
			if tt.wantReports > 0 {
				assert.EqualValues(t, tt.wantLastDelta, rep.sent[len(rep.sent)-1].PollCount)
			}
		})
	}
}

func TestAgent_step_FailedReportKeepsThePolls(t *testing.T) {
	rep := &stubReporter{err: errors.New("server is down")}
	a := New(NewCollector(), rep, 2*time.Second, 10*time.Second, discard)

	for i := 1; i <= 10; i++ {
		a.step(i)
	}

	require.Len(t, rep.sent, 2)
	assert.EqualValues(t, 5, rep.sent[0].PollCount)
	assert.EqualValues(t, 10, rep.sent[1].PollCount, "nothing was acknowledged, so the delta keeps growing")
}

func TestAgent_step_RecoversAfterAFailedReport(t *testing.T) {
	rep := &stubReporter{err: errors.New("server is down")}
	a := New(NewCollector(), rep, 2*time.Second, 10*time.Second, discard)

	for i := 1; i <= 5; i++ {
		a.step(i)
	}
	rep.err = nil
	for i := 6; i <= 10; i++ {
		a.step(i)
	}

	require.Len(t, rep.sent, 2)
	assert.EqualValues(t, 10, rep.sent[1].PollCount, "the unacknowledged polls are delivered later")
}

func pollCountOf(t *testing.T, list []models.Metrics) int64 {
	t.Helper()

	for _, m := range list {
		if m.ID == "PollCount" {
			require.NotNil(t, m.Delta)
			return *m.Delta
		}
	}
	require.Fail(t, "no PollCount in the batch")
	return 0
}

func TestAgent_step_RejectedBatchKeepsThePollsUntilOneLands(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusInternalServerError)
	a := New(NewCollector(), sender, time.Second, time.Second, discard)

	a.step(1)
	a.step(2)

	require.Len(t, rec.got, 2)
	assert.EqualValues(t, 2, pollCountOf(t, decodeBody(t, rec.got[1].body)))
	assert.EqualValues(t, 2, a.collector.Snapshot().PollCount, "Reported must not run after a rejected batch")

	rec.status = http.StatusOK
	a.step(3)

	require.Len(t, rec.got, 3)
	assert.EqualValues(t, 3, pollCountOf(t, decodeBody(t, rec.got[2].body)))
	assert.EqualValues(t, 0, a.collector.Snapshot().PollCount, "Reported runs once the batch landed")
}

func TestAgent_step_UnreachableServerKeepsThePollsAfterEveryDial(t *testing.T) {
	sender := NewSender("http://" + closedPort(t))
	sender.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	dials := countDials(sender)
	a := New(NewCollector(), sender, time.Second, time.Second, discard)

	a.step(1)

	assert.EqualValues(t, 4, dials.Load())
	assert.EqualValues(t, 1, a.collector.Snapshot().PollCount, "Reported must not run after the last dial failed")
}
