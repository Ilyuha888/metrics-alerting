// External test package: everything goes through NewRouter, as for a real caller.
package handler_test

import "github.com/Ilyuha888/metrics-alerting/internal/metrics"

type call struct {
	kind  metrics.Kind
	name  string
	gauge metrics.Gauge
	count metrics.Counter
}

// fakeStorage records writes and serves reads from preset maps; readErr fails every read.
type fakeStorage struct {
	calls    []call
	gauges   map[string]metrics.Gauge
	counters map[string]metrics.Counter
	readErr  error
}

func (f *fakeStorage) SetGauge(name string, v metrics.Gauge) {
	f.calls = append(f.calls, call{kind: metrics.KindGauge, name: name, gauge: v})
}

func (f *fakeStorage) AddCounter(name string, v metrics.Counter) {
	f.calls = append(f.calls, call{kind: metrics.KindCounter, name: name, count: v})
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
