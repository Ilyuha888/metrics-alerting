// Package memstorage keeps metric values for the lifetime of the process.
package memstorage

import (
	"context"
	"maps"
	"sync"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

// Storage keeps gauges and counters in separate maps. A metric is
// therefore identified by name and kind together.
type Storage struct {
	mu       sync.Mutex
	gauges   map[string]metrics.Gauge
	counters map[string]metrics.Counter
	// stamps holds, per gauge, the stamp of the batch that last wrote it. A write without a stamp
	// keeps it: such a write proves no time, and clearing would let any older stamped batch through.
	stamps map[string]int64
}

func New() *Storage {
	return &Storage{
		gauges:   make(map[string]metrics.Gauge),
		counters: make(map[string]metrics.Counter),
		stamps:   make(map[string]int64),
	}
}

func (s *Storage) SetGauge(_ context.Context, name string, v metrics.Gauge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gauges[name] = v
	return nil
}

func (s *Storage) AddCounter(_ context.Context, name string, v metrics.Counter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters[name] += v
	return nil
}

func (s *Storage) Gauge(_ context.Context, name string) (metrics.Gauge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.gauges[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

func (s *Storage) Counter(_ context.Context, name string) (metrics.Counter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.counters[name]
	if !ok {
		return 0, metrics.ErrNotFound
	}
	return v, nil
}

// UpdateBatch holds the lock for the whole batch, so a Snapshot cannot see half of it, and the stamp
// check and the write it allows are one step.
func (s *Storage) UpdateBatch(_ context.Context, b metrics.Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, v := range b.Gauges {
		if b.Stamp != 0 {
			if last, ok := s.stamps[name]; ok && b.Stamp <= last {
				continue
			}
			s.stamps[name] = b.Stamp
		}
		s.gauges[name] = v
	}
	for name, v := range b.Counters {
		s.counters[name] += v
	}
	return nil
}

// Snapshot copies both maps, so the caller never holds live state.
func (s *Storage) Snapshot(_ context.Context) (metrics.Snapshot, error) {
	snap, _ := s.SnapshotWithStamps()
	return snap, nil
}

// SnapshotWithStamps also returns the gauges' stamps, read under the same lock as the values, so
// a file that stores both never pairs a value with another batch's stamp.
func (s *Storage) SnapshotWithStamps() (metrics.Snapshot, map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return metrics.Snapshot{
		Gauges:   maps.Clone(s.gauges),
		Counters: maps.Clone(s.counters),
	}, maps.Clone(s.stamps)
}
