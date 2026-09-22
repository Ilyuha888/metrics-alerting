// Package storage keeps metric values for the lifetime of the process.
package storage

import "github.com/Ilyuha888/metrics-alerting/internal/metrics"

// MemStorage keeps gauges and counters in separate maps because Go has no sum types:
// one map would force an `any` value with a type assertion on every read. A metric is
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
