// External test package: everything goes through NewRouter, as for a real caller.
package handler_test

import (
	"context"
	"log/slog"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

var discardLogger = slog.New(slog.DiscardHandler)

type call struct {
	kind  metrics.Kind
	name  string
	gauge metrics.Gauge
	count metrics.Counter
}

// fakeStorage records writes and applies them to the maps, since POST /update answers with
// a read after the write; readErr fails every read. writeErr fails every write after applying
// it, as filestore keeps the value in memory when its save fails. ctxErrs holds the context's
// error at each call: once the handler returns, a deferred cancel would hide what the call saw.
type fakeStorage struct {
	calls    []call
	gauges   map[string]metrics.Gauge
	counters map[string]metrics.Counter
	readErr  error
	writeErr error
	ctxErrs  []error
}

func (f *fakeStorage) SetGauge(ctx context.Context, name string, v metrics.Gauge) error {
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.calls = append(f.calls, call{kind: metrics.KindGauge, name: name, gauge: v})
	if f.gauges == nil {
		f.gauges = make(map[string]metrics.Gauge)
	}
	f.gauges[name] = v
	return f.writeErr
}

func (f *fakeStorage) AddCounter(ctx context.Context, name string, v metrics.Counter) error {
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	f.calls = append(f.calls, call{kind: metrics.KindCounter, name: name, count: v})
	if f.counters == nil {
		f.counters = make(map[string]metrics.Counter)
	}
	f.counters[name] += v
	return f.writeErr
}

func (f *fakeStorage) Gauge(ctx context.Context, name string) (metrics.Gauge, error) {
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	if f.readErr != nil {
		return 0, f.readErr
	}
	v, ok := f.gauges[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (f *fakeStorage) Counter(ctx context.Context, name string) (metrics.Counter, error) {
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	if f.readErr != nil {
		return 0, f.readErr
	}
	v, ok := f.counters[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (f *fakeStorage) Snapshot(ctx context.Context) (metrics.Snapshot, error) {
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	if f.readErr != nil {
		return metrics.Snapshot{}, f.readErr
	}
	return metrics.Snapshot{Gauges: f.gauges, Counters: f.counters}, nil
}
