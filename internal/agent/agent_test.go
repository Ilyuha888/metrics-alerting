package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubReporter struct {
	sent []Snapshot
	err  error
}

func (s *stubReporter) Send(_ context.Context, snap Snapshot) error {
	s.sent = append(s.sent, snap)
	return s.err
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// step is driven directly instead of Run, which waits on the clock and returns only on a cancel.
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
				a.step(t.Context(), i)
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
		a.step(t.Context(), i)
	}

	require.Len(t, rep.sent, 2)
	assert.EqualValues(t, 5, rep.sent[0].PollCount)
	assert.EqualValues(t, 10, rep.sent[1].PollCount, "nothing was acknowledged, so the delta keeps growing")
}

func TestAgent_step_RecoversAfterAFailedReport(t *testing.T) {
	rep := &stubReporter{err: errors.New("server is down")}
	a := New(NewCollector(), rep, 2*time.Second, 10*time.Second, discard)

	for i := 1; i <= 5; i++ {
		a.step(t.Context(), i)
	}
	rep.err = nil
	for i := 6; i <= 10; i++ {
		a.step(t.Context(), i)
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

	a.step(t.Context(), 1)
	a.step(t.Context(), 2)

	require.Len(t, rec.got, 2)
	assert.EqualValues(t, 2, pollCountOf(t, decodeBody(t, rec.got[1].body)))
	assert.EqualValues(t, 2, a.collector.Snapshot().PollCount, "Reported must not run after a rejected batch")

	rec.status = http.StatusOK
	a.step(t.Context(), 3)

	require.Len(t, rec.got, 3)
	assert.EqualValues(t, 3, pollCountOf(t, decodeBody(t, rec.got[2].body)))
	assert.EqualValues(t, 0, a.collector.Snapshot().PollCount, "Reported runs once the batch landed")
}

func TestAgent_step_UnreachableServerKeepsThePollsAfterEveryDial(t *testing.T) {
	sender := NewSender("http://" + closedPort(t))
	sender.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	dials := countDials(sender)
	a := New(NewCollector(), sender, time.Second, time.Second, discard)

	a.step(t.Context(), 1)

	assert.EqualValues(t, 4, dials.Load())
	assert.EqualValues(t, 1, a.collector.Snapshot().PollCount, "Reported must not run after the last dial failed")
}

type sendCall struct {
	snap        Snapshot
	at          time.Duration
	ctxErr      error
	hasDeadline bool
	timeLeft    time.Duration
}

// scriptedReporter records what each Send saw while it ran: lastReport cancels its context on return.
type scriptedReporter struct {
	start  time.Time
	onSend func(call int) error
	calls  []sendCall
}

func (r *scriptedReporter) Send(ctx context.Context, snap Snapshot) error {
	deadline, hasDeadline := ctx.Deadline()
	r.calls = append(r.calls, sendCall{
		snap:        snap,
		at:          time.Since(r.start),
		ctxErr:      ctx.Err(),
		hasDeadline: hasDeadline,
		timeLeft:    time.Until(deadline),
	})
	if r.onSend == nil {
		return nil
	}
	return r.onSend(len(r.calls))
}

func assertIsLastReport(t *testing.T, call sendCall) {
	t.Helper()

	assert.NoError(t, call.ctxErr, "the last report must not run on the cancelled context")
	assert.True(t, call.hasDeadline)
	assert.Equal(t, sendTimeout, call.timeLeft)
}

func errCutPause() error {
	return fmt.Errorf("stopped after attempt 1: %w: %w", context.Canceled, errors.New("connection refused"))
}

func TestAgent_Run_PollsAndReportsOnTheInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(7*time.Second, cancel)
		rep := &scriptedReporter{start: time.Now()}

		New(NewCollector(), rep, 2*time.Second, 2*time.Second, discard).Run(ctx)

		require.Len(t, rep.calls, 4)
		for i, wantAt := range []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second, 7 * time.Second} {
			assert.Equal(t, wantAt, rep.calls[i].at)
		}
	})
}

func TestAgent_Run_CancelWhileWaitingSendsTheLastReport(t *testing.T) {
	tests := []struct {
		name      string
		cancelAt  time.Duration
		wantPolls int64
	}{
		{name: "before the first poll", cancelAt: 500 * time.Millisecond, wantPolls: 0},
		{name: "between polls", cancelAt: 2500 * time.Millisecond, wantPolls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				time.AfterFunc(tt.cancelAt, cancel)
				rep := &scriptedReporter{start: time.Now()}
				a := New(NewCollector(), rep, time.Second, time.Hour, discard)

				a.Run(ctx)

				assert.Equal(t, tt.cancelAt, time.Since(rep.start))
				require.Len(t, rep.calls, 1)
				assert.EqualValues(t, tt.wantPolls, rep.calls[0].snap.PollCount)
				assertIsLastReport(t, rep.calls[0])
				assert.EqualValues(t, tt.wantPolls, a.collector.Snapshot().PollCount, "no step runs after the cancel")
			})
		})
	}
}

func TestAgent_Run_ContextDoneAsTheTimerFiresRunsNoPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 100 {
		rep := &stubReporter{}

		New(NewCollector(), rep, time.Nanosecond, time.Hour, discard).Run(ctx)

		require.Len(t, rep.sent, 1)
		require.EqualValues(t, 0, rep.sent[0].PollCount)
	}
}

func TestAgent_Run_CancelDuringASendSendsALastReportOnlyAfterASuccess(t *testing.T) {
	tests := []struct {
		name         string
		sendErr      error
		wantSends    int
		wantLastPoll int64
	}{
		{name: "send that succeeds is reported, then the last report goes", sendErr: nil, wantSends: 2, wantLastPoll: 0},
		{name: "send that fails stops without a last report", sendErr: errors.New("EOF"), wantSends: 1, wantLastPoll: 2},
		{name: "send whose pause is cut stops without a last report", sendErr: errCutPause(), wantSends: 1, wantLastPoll: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rep := &scriptedReporter{start: time.Now(), onSend: func(call int) error {
					if call == 1 {
						cancel()
						return tt.sendErr
					}
					return nil
				}}

				New(NewCollector(), rep, time.Second, 2*time.Second, discard).Run(ctx)

				assert.Equal(t, 2*time.Second, time.Since(rep.start))
				require.Len(t, rep.calls, tt.wantSends)
				assert.EqualValues(t, tt.wantLastPoll, rep.calls[len(rep.calls)-1].snap.PollCount)
				if tt.wantSends == 2 {
					assertIsLastReport(t, rep.calls[1])
				}
			})
		})
	}
}

func TestAgent_Run_FailedSendThenCancelWhileWaitingSendsTheLastReport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(2500*time.Millisecond, cancel)
		rep := &scriptedReporter{start: time.Now(), onSend: func(call int) error {
			if call == 1 {
				return errors.New("EOF")
			}
			return nil
		}}

		New(NewCollector(), rep, time.Second, 2*time.Second, discard).Run(ctx)

		require.Len(t, rep.calls, 2)
		assert.EqualValues(t, 2, rep.calls[1].snap.PollCount, "nothing was acknowledged, so the last report carries it")
		assertIsLastReport(t, rep.calls[1])
	})
}

func TestAgent_Run_LogsTheStopAndOnlyRealFailures(t *testing.T) {
	const stopped = `level=INFO msg="agent stopped"`
	const sendFailed = `level=ERROR msg="send metrics"`
	const lastFailed = `level=ERROR msg="send last report"`

	tests := []struct {
		name     string
		cancelAt time.Duration
		sendErr  error
		want     []string
		wantNot  []string
	}{
		{
			name:     "cancel while waiting",
			cancelAt: 500 * time.Millisecond,
			wantNot:  []string{sendFailed, lastFailed},
		},
		{
			name:    "pause cut during a send",
			sendErr: errCutPause(),
			wantNot: []string{sendFailed, lastFailed},
		},
		{
			name:    "real failure during a send",
			sendErr: errors.New("EOF"),
			want:    []string{sendFailed},
			wantNot: []string{lastFailed},
		},
		{
			name:     "failed last report",
			cancelAt: 500 * time.Millisecond,
			sendErr:  errors.New("EOF"),
			want:     []string{lastFailed},
			wantNot:  []string{sendFailed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logged bytes.Buffer
				lg := slog.New(slog.NewTextHandler(&logged, nil))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tt.cancelAt > 0 {
					time.AfterFunc(tt.cancelAt, cancel)
				}
				rep := &scriptedReporter{start: time.Now(), onSend: func(int) error {
					cancel()
					return tt.sendErr
				}}

				New(NewCollector(), rep, time.Second, time.Second, lg).Run(ctx)

				assert.Contains(t, logged.String(), stopped)
				for _, line := range tt.want {
					assert.Contains(t, logged.String(), line)
				}
				for _, line := range tt.wantNot {
					assert.NotContains(t, logged.String(), line)
				}
			})
		})
	}
}

func TestAgent_Run_PauseCutOnARealSenderLogsNoErrorAndDialsOnce(t *testing.T) {
	var logged bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&logged, nil))
	sender := NewSender("http://" + closedPort(t))
	sender.SetDelays([]time.Duration{time.Hour})
	dials := countDials(sender)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})

	go func() {
		defer close(done)
		New(NewCollector(), sender, time.Millisecond, time.Millisecond, lg).Run(ctx)
	}()
	require.Eventually(t, func() bool { return dials.Load() == 1 }, 2*time.Second, time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		require.Fail(t, "Run did not return on the cancel")
	}
	assert.EqualValues(t, 1, dials.Load(), "neither a repeat nor a last report")
	assert.Contains(t, logged.String(), `msg="agent stopped"`)
	assert.NotContains(t, logged.String(), "level=ERROR")
}

func TestAgent_step_ClientTimeoutIsLoggedAsAFailure(t *testing.T) {
	var logged bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&logged, nil))
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	sender := NewSender(srv.URL)
	sender.client.Timeout = 100 * time.Millisecond
	a := New(NewCollector(), sender, time.Second, time.Second, lg)

	require.Error(t, a.step(t.Context(), 1))

	assert.Contains(t, logged.String(), `level=ERROR msg="send metrics"`)
}
