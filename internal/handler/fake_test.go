// External test package: everything goes through NewRouter, as for a real caller.
package handler_test

import (
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
// it, as filestore keeps the value in memory when its save fails.
type fakeStorage struct {
	calls    []call
	gauges   map[string]metrics.Gauge
	counters map[string]metrics.Counter
	readErr  error
	writeErr error
}

func (f *fakeStorage) SetGauge(name string, v metrics.Gauge) error {
	f.calls = append(f.calls, call{kind: metrics.KindGauge, name: name, gauge: v})
	if f.gauges == nil {
		f.gauges = make(map[string]metrics.Gauge)
	}
	f.gauges[name] = v
	return f.writeErr
}

func (f *fakeStorage) AddCounter(name string, v metrics.Counter) error {
	f.calls = append(f.calls, call{kind: metrics.KindCounter, name: name, count: v})
	if f.counters == nil {
		f.counters = make(map[string]metrics.Counter)
	}
	f.counters[name] += v
	return f.writeErr
}

func (f *fakeStorage) Gauge(name string) (metrics.Gauge, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	v, ok := f.gauges[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (f *fakeStorage) Counter(name string) (metrics.Counter, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	v, ok := f.counters[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (f *fakeStorage) Snapshot() metrics.Snapshot {
	return metrics.Snapshot{Gauges: f.gauges, Counters: f.counters}
}
