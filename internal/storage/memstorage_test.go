// Internal test package: MemStorage exposes no reads to assert through.
package storage

import (
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func TestMemStorage_SetGauge(t *testing.T) {
	tests := []struct {
		name    string
		reports []metrics.Gauge
		want    metrics.Gauge
	}{
		{name: "first report is stored", reports: []metrics.Gauge{36.6}, want: 36.6},
		{name: "later report replaces", reports: []metrics.Gauge{36.6, 37.2}, want: 37.2},
		{name: "zero replaces", reports: []metrics.Gauge{36.6, 0}, want: 0},
		{name: "negative is allowed", reports: []metrics.Gauge{-1.5}, want: -1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemStorage()
			for _, v := range tt.reports {
				s.SetGauge("temp", v)
			}
			assert.Equal(t, tt.want, s.gauges["temp"])
		})
	}
}

func TestMemStorage_AddCounter(t *testing.T) {
	tests := []struct {
		name    string
		reports []metrics.Counter
		want    metrics.Counter
	}{
		{name: "unknown name starts from zero", reports: []metrics.Counter{527}, want: 527},
		{name: "reports accumulate", reports: []metrics.Counter{527, 3}, want: 530},
		{name: "zero keeps the value", reports: []metrics.Counter{527, 0}, want: 527},
		{name: "negative subtracts", reports: []metrics.Counter{527, -27}, want: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemStorage()
			for _, v := range tt.reports {
				s.AddCounter("hits", v)
			}
			assert.Equal(t, tt.want, s.counters["hits"])
		})
	}
}

func TestMemStorage_NamesAndKindsAreIndependent(t *testing.T) {
	s := NewMemStorage()
	s.SetGauge("same", 1.5)
	s.AddCounter("same", 7)

	assert.Equal(t, metrics.Gauge(1.5), s.gauges["same"])
	assert.Equal(t, metrics.Counter(7), s.counters["same"])
}
