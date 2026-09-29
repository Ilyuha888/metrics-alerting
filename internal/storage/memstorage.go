// Package storage keeps metric values for the lifetime of the process.
package storage

import (
	"maps"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

// MemStorage keeps gauges and counters in separate maps. A metric is
// therefore identified by name and kind together.
//
// Not safe for concurrent use.
type MemStorage struct {
	gauges   map[string]metrics.Gauge
	counters map[string]metrics.Counter
}

func NewMemStorage() *MemStorage {
	return &MemStorage{
		gauges:   make(map[string]metrics.Gauge),
		counters: make(map[string]metrics.Counter),
	}
}

func (s *MemStorage) SetGauge(name string, v metrics.Gauge) {
	s.gauges[name] = v
}

func (s *MemStorage) AddCounter(name string, v metrics.Counter) {
	s.counters[name] += v
}

func (s *MemStorage) Gauge(name string) (metrics.Gauge, error) {
	v, ok := s.gauges[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (s *MemStorage) Counter(name string) (metrics.Counter, error) {
	v, ok := s.counters[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

// Snapshot copies both maps, so the caller never holds live state.
func (s *MemStorage) Snapshot() metrics.Snapshot {
	return metrics.Snapshot{
		Gauges:   maps.Clone(s.gauges),
		Counters: maps.Clone(s.counters),
	}
}
