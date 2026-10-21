// Package storage keeps metric values for the lifetime of the process.
package storage

import (
	"maps"
	"sync"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

// MemStorage keeps gauges and counters in separate maps. A metric is
// therefore identified by name and kind together.
type MemStorage struct {
	mu       sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gauges[name] = v
}

func (s *MemStorage) AddCounter(name string, v metrics.Counter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters[name] += v
}

func (s *MemStorage) Gauge(name string) (metrics.Gauge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.gauges[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (s *MemStorage) Counter(name string) (metrics.Counter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.counters[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

// Snapshot copies both maps, so the caller never holds live state.
func (s *MemStorage) Snapshot() metrics.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return metrics.Snapshot{
		Gauges:   maps.Clone(s.gauges),
		Counters: maps.Clone(s.counters),
	}
}
