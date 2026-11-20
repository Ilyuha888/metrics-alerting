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
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
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

func config(poll, report time.Duration, rateLimit int) Config {
	return Config{PollInterval: poll, ReportInterval: report, RateLimit: rateLimit}
}

func newAgent(t *testing.T, r reporter, cfg Config, lg *slog.Logger) *Agent {
	t.Helper()
	a, err := New(NewCollector(), NewSystemCollector(), r, cfg, lg)
	require.NoError(t, err)
	return a
}

func TestNew_NonPositiveConfig_ReturnsErrorNamingTheField(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "zero poll interval", cfg: config(0, time.Second, 1), wantErr: "PollInterval must be positive, got 0s"},
		{name: "negative poll interval", cfg: config(-time.Second, time.Second, 1), wantErr: "PollInterval must be positive, got -1s"},
		{name: "zero report interval", cfg: config(time.Second, 0, 1), wantErr: "ReportInterval must be positive, got 0s"},
		{name: "negative report interval", cfg: config(time.Second, -time.Second, 1), wantErr: "ReportInterval must be positive, got -1s"},
		{name: "zero rate limit", cfg: config(time.Second, time.Second, 0), wantErr: "RateLimit must be positive, got 0"},
		{name: "negative rate limit", cfg: config(time.Second, time.Second, -1), wantErr: "RateLimit must be positive, got -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(NewCollector(), NewSystemCollector(), &stubReporter{}, tt.cfg, discard)

			assert.Nil(t, a)
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestNew_SmallestPositiveConfig_ReturnsAgent(t *testing.T) {
	a, err := New(NewCollector(), NewSystemCollector(), &stubReporter{}, config(time.Nanosecond, time.Nanosecond, 1), discard)

	require.NoError(t, err)
	assert.NotNil(t, a)
}

func pending(c *Collector) metrics.Counter {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.pollCount
}

func pollAndSend(t *testing.T, a *Agent) error {
	t.Helper()

	a.collector.Poll()
	return a.send(t.Context(), a.snapshot())
}

func TestAgent_send_FailedReportKeepsThePolls(t *testing.T) {
	rep := &stubReporter{err: errors.New("server is down")}
	a := newAgent(t, rep, config(time.Second, time.Second, 1), discard)

	for range 5 {
		a.collector.Poll()
	}
	require.Error(t, a.send(t.Context(), a.snapshot()))
	for range 5 {
		a.collector.Poll()
	}
	require.Error(t, a.send(t.Context(), a.snapshot()))

	require.Len(t, rep.sent, 2)
	assert.EqualValues(t, 5, rep.sent[0].PollCount)
	assert.EqualValues(t, 10, rep.sent[1].PollCount, "nothing was delivered, so the delta keeps growing")
}

func TestAgent_send_RecoversAfterAFailedReport(t *testing.T) {
	rep := &stubReporter{err: errors.New("server is down")}
	a := newAgent(t, rep, config(time.Second, time.Second, 1), discard)

	for range 5 {
		a.collector.Poll()
	}
	require.Error(t, a.send(t.Context(), a.snapshot()))
	rep.err = nil
	for range 5 {
		a.collector.Poll()
	}
	require.NoError(t, a.send(t.Context(), a.snapshot()))

	require.Len(t, rep.sent, 2)
	assert.EqualValues(t, 10, rep.sent[1].PollCount, "the undelivered polls are delivered later")
	assert.EqualValues(t, 0, pending(a.collector))
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

func TestAgent_send_RejectedBatchKeepsThePollsUntilOneLands(t *testing.T) {
	sender, rec := recordingServer(t, http.StatusInternalServerError)
	a := newAgent(t, sender, config(time.Second, time.Second, 1), discard)

	require.Error(t, pollAndSend(t, a))
	require.Error(t, pollAndSend(t, a))

	require.Len(t, rec.got, 2)
	assert.EqualValues(t, 2, pollCountOf(t, decodeBody(t, rec.got[1].body)))
	assert.EqualValues(t, 2, pending(a.collector), "a rejected batch must restore its delta")

	rec.status = http.StatusOK
	require.NoError(t, pollAndSend(t, a))

	require.Len(t, rec.got, 3)
	assert.EqualValues(t, 3, pollCountOf(t, decodeBody(t, rec.got[2].body)))
	assert.EqualValues(t, 0, pending(a.collector), "a delivered batch must not restore its delta")
}

func TestAgent_send_UnreachableServerKeepsThePollsAfterEveryDial(t *testing.T) {
	sender := NewSender(unreachable, nil)
	sender.SetDelays([]time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	dials := refuseDials(sender, everyDial)
	a := newAgent(t, sender, config(time.Second, time.Second, 1), discard)

	require.Error(t, pollAndSend(t, a))

	assert.EqualValues(t, 4, dials.Load())
	assert.EqualValues(t, 1, pending(a.collector), "the delta must come back after the last dial failed")
}

func TestAgent_send_ClientTimeoutIsLoggedAsAFailure(t *testing.T) {
	var logged bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&logged, nil))
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	sender := NewSender(srv.URL, nil)
	sender.client.Timeout = 100 * time.Millisecond
	a := newAgent(t, sender, config(time.Second, time.Second, 1), lg)

	require.Error(t, pollAndSend(t, a))

	assert.Contains(t, logged.String(), `level=ERROR msg="send metrics"`)
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
	mu     sync.Mutex
	calls  []sendCall
}

func (r *scriptedReporter) Send(ctx context.Context, snap Snapshot) error {
	deadline, hasDeadline := ctx.Deadline()
	r.mu.Lock()
	r.calls = append(r.calls, sendCall{
		snap:        snap,
		at:          time.Since(r.start),
		ctxErr:      ctx.Err(),
		hasDeadline: hasDeadline,
		timeLeft:    time.Until(deadline),
	})
	call := len(r.calls)
	r.mu.Unlock()

	if r.onSend == nil {
		return nil
	}
	return r.onSend(call)
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

		newAgent(t, rep, config(2*time.Second, 2*time.Second, 1), discard).Run(ctx)

		require.Len(t, rep.calls, 4)
		for i, wantAt := range []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second, 7 * time.Second} {
			assert.Equal(t, wantAt, rep.calls[i].at)
		}
	})
}

func TestAgent_Run_ReportsOnTheReportIntervalNotOnPolls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(350*time.Millisecond, cancel)
		rep := &scriptedReporter{start: time.Now()}

		newAgent(t, rep, config(30*time.Millisecond, 100*time.Millisecond, 1), discard).Run(ctx)

		require.Len(t, rep.calls, 4)
		for i, wantAt := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond, 350 * time.Millisecond} {
			assert.Equal(t, wantAt, rep.calls[i].at)
		}
	})
}

func TestAgent_Run_EachReportIsStampedWhenTaken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(250*time.Millisecond, cancel)
		rep := &scriptedReporter{start: time.Now()}

		newAgent(t, rep, config(30*time.Millisecond, 100*time.Millisecond, 1), discard).Run(ctx)

		require.Len(t, rep.calls, 3)
		for i, wantAt := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 250 * time.Millisecond} {
			assert.Equal(t, rep.start.Add(wantAt), rep.calls[i].snap.Taken, "report %d", i+1)
		}
	})
}

func TestAgent_Run_ReportCarriesRuntimeAndSystemGauges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(3*time.Second, cancel)
		rep := &scriptedReporter{start: time.Now()}

		newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 1), discard).Run(ctx)

		require.NotEmpty(t, rep.calls)
		gauges := rep.calls[0].snap.Gauges
		for _, name := range []string{"Alloc", "RandomValue", "TotalMemory", "FreeMemory", "CPUutilization1"} {
			assert.Contains(t, gauges, name)
		}
	})
}

type blockingReporter struct {
	release  chan struct{}
	mu       sync.Mutex
	started  int
	inFlight int
	maxSeen  int
}

func (r *blockingReporter) Send(context.Context, Snapshot) error {
	r.mu.Lock()
	r.started++
	r.inFlight++
	r.maxSeen = max(r.maxSeen, r.inFlight)
	r.mu.Unlock()

	<-r.release

	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()
	return nil
}

func TestAgent_Run_KeepsSendsInFlightAtTheRateLimit(t *testing.T) {
	for _, limit := range []int{1, 3} {
		t.Run(fmt.Sprintf("rate limit %d", limit), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rep := &blockingReporter{release: make(chan struct{})}
				done := make(chan struct{})

				a := newAgent(t, rep, config(time.Second, time.Second, limit), discard)
				go func() {
					defer close(done)
					a.Run(ctx)
				}()
				time.Sleep(time.Duration(limit+5)*time.Second + 500*time.Millisecond)
				synctest.Wait()

				rep.mu.Lock()
				assert.Equal(t, limit, rep.inFlight, "every worker is busy")
				assert.Equal(t, limit, rep.started, "the reporter waits for a free worker")
				rep.mu.Unlock()

				close(rep.release)
				cancel()
				<-done
				assert.Equal(t, limit, rep.maxSeen)
			})
		})
	}
}

func TestAgent_Run_PollsWhileASendIsSlow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		rep := &blockingReporter{release: make(chan struct{})}
		a := newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 1), discard)
		done := make(chan struct{})

		go func() {
			defer close(done)
			a.Run(ctx)
		}()
		time.Sleep(4900 * time.Millisecond)
		synctest.Wait()

		rep.mu.Lock()
		require.Equal(t, 1, rep.inFlight, "the report taken at 2.5 s is still being sent")
		rep.mu.Unlock()
		assert.EqualValues(t, 2, pending(a.collector), "polls at 3 s and 4 s ran during the send")

		close(rep.release)
		cancel()
		<-done
	})
}

func TestAgent_Run_EachPollIsCountedOnceWithSendsInFlight(t *testing.T) {
	tests := []struct {
		name     string
		firstErr error
	}{
		{name: "both sends succeed", firstErr: nil},
		{name: "first send fails and its delta goes with a later one", firstErr: errors.New("EOF")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				time.AfterFunc(7700*time.Millisecond, cancel)
				secondStarted := make(chan struct{})
				rep := &scriptedReporter{start: time.Now()}
				rep.onSend = func(call int) error {
					switch call {
					case 1:
						<-secondStarted
						return tt.firstErr
					case 2:
						close(secondStarted)
					}
					return nil
				}

				newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 2), discard).Run(ctx)

				require.Len(t, rep.calls, 4)
				assert.EqualValues(t, 2, rep.calls[0].snap.PollCount)
				var delivered metrics.Counter
				for i, call := range rep.calls {
					if i == 0 && tt.firstErr != nil {
						continue
					}
					delivered += call.snap.PollCount
				}
				assert.EqualValues(t, 7, delivered, "polls at 1 s through 7 s, each delivered once")
			})
		})
	}
}

func TestAgent_Run_CancelDuringASendWaitsForItThenReportsTheRest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		release := make(chan struct{})
		rep := &scriptedReporter{start: time.Now(), onSend: func(call int) error {
			if call == 1 {
				<-release
			}
			return nil
		}}
		done := make(chan struct{})

		a := newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 1), discard)
		go func() {
			defer close(done)
			a.Run(ctx)
		}()
		time.Sleep(3200 * time.Millisecond)
		cancel()
		time.Sleep(time.Second)
		synctest.Wait()

		select {
		case <-done:
			require.Fail(t, "Run returned with a send in flight")
		default:
		}
		close(release)
		<-done

		require.Len(t, rep.calls, 2)
		assert.EqualValues(t, 2, rep.calls[0].snap.PollCount)
		assert.Equal(t, 4200*time.Millisecond, rep.calls[1].at, "the last report waits for the send in flight")
		assert.EqualValues(t, 1, rep.calls[1].snap.PollCount, "the poll at 3 s")
		assertIsLastReport(t, rep.calls[1])
	})
}

func TestAgent_Run_CancelWhileTheReporterWaitsForAWorkerKeepsItsSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		release := make(chan struct{})
		rep := &scriptedReporter{start: time.Now(), onSend: func(call int) error {
			if call == 1 {
				<-release
			}
			return nil
		}}
		done := make(chan struct{})

		a := newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 1), discard)
		go func() {
			defer close(done)
			a.Run(ctx)
		}()
		time.Sleep(5500 * time.Millisecond)
		cancel()
		synctest.Wait()
		close(release)
		<-done

		require.Len(t, rep.calls, 2, "the snapshot no worker took is not sent on its own")
		assert.EqualValues(t, 2, rep.calls[0].snap.PollCount)
		assert.EqualValues(t, 3, rep.calls[1].snap.PollCount, "polls at 3 s, 4 s and 5 s")
		assertIsLastReport(t, rep.calls[1])
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
				a := newAgent(t, rep, config(time.Second, time.Hour, 1), discard)

				a.Run(ctx)

				assert.Equal(t, tt.cancelAt, time.Since(rep.start))
				require.Len(t, rep.calls, 1)
				assert.EqualValues(t, tt.wantPolls, rep.calls[0].snap.PollCount)
				assertIsLastReport(t, rep.calls[0])
				assert.EqualValues(t, 0, pending(a.collector), "no poll runs after the cancel")
			})
		})
	}
}

func TestAgent_Run_ContextDoneAsTheTimerFiresRunsNoPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 100 {
		rep := &stubReporter{}

		newAgent(t, rep, config(time.Nanosecond, time.Hour, 1), discard).Run(ctx)

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

				newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 1), discard).Run(ctx)

				assert.Equal(t, 2500*time.Millisecond, time.Since(rep.start))
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
		time.AfterFunc(3200*time.Millisecond, cancel)
		rep := &scriptedReporter{start: time.Now(), onSend: func(call int) error {
			if call == 1 {
				return errors.New("EOF")
			}
			return nil
		}}

		newAgent(t, rep, config(time.Second, 2500*time.Millisecond, 1), discard).Run(ctx)

		require.Len(t, rep.calls, 2)
		assert.EqualValues(t, 3, rep.calls[1].snap.PollCount, "nothing was delivered, so the last report carries it")
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

				newAgent(t, rep, config(time.Second, time.Second, 1), lg).Run(ctx)

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
	sender := NewSender(unreachable, nil)
	sender.SetDelays([]time.Duration{time.Hour})
	dials := refuseDials(sender, everyDial)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})

	a := newAgent(t, sender, config(time.Millisecond, time.Millisecond, 1), lg)
	go func() {
		defer close(done)
		a.Run(ctx)
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
