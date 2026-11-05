package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type reporter interface {
	Send(ctx context.Context, snap Snapshot) error
}

type Agent struct {
	collector    *Collector
	reporter     reporter
	pollInterval time.Duration
	everyN       int
	log          *slog.Logger
}

// New reports once every everyN polls; a reportInterval that is not a multiple of
// pollInterval rounds down.
func New(c *Collector, r reporter, pollInterval, reportInterval time.Duration, lg *slog.Logger) *Agent {
	everyN := int(reportInterval / pollInterval)
	if everyN < 1 {
		everyN = 1
	}
	return &Agent{collector: c, reporter: r, pollInterval: pollInterval, everyN: everyN, log: lg}
}

// Run returns once ctx is done. One goroutine does both jobs, which is why the collector needs no
// lock; splitting them wants a mutex, and that waits for the sprint teaching it.
func (a *Agent) Run(ctx context.Context) {
	defer a.log.Info("agent stopped")

	for i := 1; ; i++ {
		if wait(ctx, a.pollInterval) != nil {
			a.lastReport(ctx)
			return
		}
		err := a.step(ctx, i)
		if ctx.Err() != nil {
			// The failed request may have landed, and a repeat would count PollCount twice.
			if err == nil {
				a.lastReport(ctx)
			}
			return
		}
	}
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	// Not only for the first case: select picks at random when both are ready, and a poll must not
	// run on a context that is already done.
	return ctx.Err()
}

func (a *Agent) step(ctx context.Context, i int) error {
	a.collector.Poll()
	if i%a.everyN != 0 {
		return nil
	}

	snap := a.collector.Snapshot()
	err := a.reporter.Send(ctx, snap)
	if err != nil {
		// The server may not be up yet; keep the polls for the next report. A cut pause is the agent
		// stopping, not a failure.
		if !errors.Is(err, context.Canceled) {
			a.log.Error("send metrics", "err", err)
		}
		return err
	}
	a.collector.Reported(snap)
	return nil
}

// lastReport needs a context of its own, since ctx is already cancelled and would cut its pauses
// at once. It skips Reported: the process is leaving.
func (a *Agent) lastReport(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()

	if err := a.reporter.Send(ctx, a.collector.Snapshot()); err != nil {
		a.log.Error("send last report", "err", err)
	}
}
