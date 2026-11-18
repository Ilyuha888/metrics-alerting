package agent

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"time"
)

type reporter interface {
	Send(ctx context.Context, snap Snapshot) error
}

type Config struct {
	PollInterval   time.Duration
	ReportInterval time.Duration
	RateLimit      int
}

type Agent struct {
	collector *Collector
	system    *SystemCollector
	reporter  reporter
	cfg       Config
	log       *slog.Logger
}

func New(c *Collector, sys *SystemCollector, r reporter, cfg Config, lg *slog.Logger) *Agent {
	return &Agent{collector: c, system: sys, reporter: r, cfg: cfg, log: lg}
}

// Run returns once ctx is done and every goroutine it started has returned.
func (a *Agent) Run(ctx context.Context) {
	defer a.log.Info("agent stopped")

	// Unbuffered: the reporter waits for a free worker, and its ticker drops the ticks it misses.
	jobs := make(chan Snapshot)
	var failedOnStop atomic.Bool
	var wg sync.WaitGroup

	wg.Go(func() { every(ctx, a.cfg.PollInterval, a.collector.Poll) })
	wg.Go(func() { every(ctx, a.cfg.PollInterval, func() { a.pollSystem(ctx) }) })
	wg.Go(func() {
		// Closed by its only sender, once no more jobs can come.
		defer close(jobs)
		every(ctx, a.cfg.ReportInterval, func() { a.enqueue(ctx, jobs) })
	})
	for range a.cfg.RateLimit {
		wg.Go(func() {
			for snap := range jobs {
				// The failed request may have landed, and a last report would count PollCount twice.
				if err := a.send(ctx, snap); err != nil && ctx.Err() != nil {
					failedOnStop.Store(true)
				}
			}
		})
	}
	wg.Wait()

	if !failedOnStop.Load() {
		a.lastReport(ctx)
	}
}

func every(ctx context.Context, d time.Duration, f func()) {
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		// Not only for the first case: select picks at random when both are ready, and f must not
		// run on a context that is already done.
		if ctx.Err() != nil {
			return
		}
		f()
	}
}

// pollSystem logs a failure and goes on: system metrics are extra, and the runtime ones still go out.
func (a *Agent) pollSystem(ctx context.Context) {
	if err := a.system.Poll(ctx); err != nil && ctx.Err() == nil {
		a.log.Error("poll system metrics", "err", err)
	}
}

func (a *Agent) snapshot() Snapshot {
	snap := a.collector.Snapshot()
	maps.Copy(snap.Gauges, a.system.Gauges())
	snap.Taken = time.Now()
	return snap
}

func (a *Agent) enqueue(ctx context.Context, jobs chan<- Snapshot) {
	snap := a.snapshot()
	select {
	case jobs <- snap:
	case <-ctx.Done():
		// No worker took it; the last report carries the delta.
		a.collector.Restore(snap)
	}
}

func (a *Agent) send(ctx context.Context, snap Snapshot) error {
	err := a.reporter.Send(ctx, snap)
	if err == nil {
		return nil
	}
	// The server may not be up yet; the next report carries the polls.
	a.collector.Restore(snap)
	// A cut pause is the agent stopping, not a failure.
	if !errors.Is(err, context.Canceled) {
		a.log.Error("send metrics", "err", err)
	}
	return err
}

// lastReport needs a context of its own, since ctx is already cancelled and would cut its pauses
// at once. A failure restores nothing: the process is leaving.
func (a *Agent) lastReport(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()

	if err := a.reporter.Send(ctx, a.snapshot()); err != nil {
		a.log.Error("send last report", "err", err)
	}
}
