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

func TestMemStorage_Gauge(t *testing.T) {
	tests := []struct {
		name    string
		stored  map[string]metrics.Gauge
		lookup  string
		want    metrics.Gauge
		wantErr error
	}{
		{name: "stored value is returned", stored: map[string]metrics.Gauge{"temp": 36.6}, lookup: "temp", want: 36.6},
		{name: "stored zero is found, not missing", stored: map[string]metrics.Gauge{"zero": 0}, lookup: "zero", want: 0},
		{name: "unknown name", stored: map[string]metrics.Gauge{"temp": 36.6}, lookup: "nope", wantErr: metrics.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemStorage()
			for name, v := range tt.stored {
				s.SetGauge(name, v)
			}

			got, err := s.Gauge(tt.lookup)

			assert.Equal(t, tt.wantErr, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMemStorage_Counter(t *testing.T) {
	tests := []struct {
		name    string
		stored  map[string]metrics.Counter
		lookup  string
		want    metrics.Counter
		wantErr error
	}{
		{name: "stored value is returned", stored: map[string]metrics.Counter{"hits": 527}, lookup: "hits", want: 527},
		{name: "stored zero is found, not missing", stored: map[string]metrics.Counter{"zero": 0}, lookup: "zero", want: 0},
		{name: "unknown name", stored: map[string]metrics.Counter{"hits": 527}, lookup: "nope", wantErr: metrics.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMemStorage()
			for name, v := range tt.stored {
				s.AddCounter(name, v)
			}

			got, err := s.Counter(tt.lookup)

			assert.Equal(t, tt.wantErr, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMemStorage_Counter_DoesNotSeeGauges(t *testing.T) {
	s := NewMemStorage()
	s.SetGauge("only_gauge", 1)

	_, err := s.Counter("only_gauge")

	assert.Equal(t, metrics.ErrNotFound, err)
}

func TestMemStorage_Snapshot_HoldsBothKinds(t *testing.T) {
	s := NewMemStorage()
	s.SetGauge("temp", 36.6)
	s.AddCounter("hits", 527)

	snap := s.Snapshot()

	assert.Equal(t, map[string]metrics.Gauge{"temp": 36.6}, snap.Gauges)
	assert.Equal(t, map[string]metrics.Counter{"hits": 527}, snap.Counters)
}

// A map value is a reference: without a copy, the snapshot and the storage would be the
// same map and a change on either side would show on the other. Both directions are checked.
func TestMemStorage_Snapshot_EditingSnapshotLeavesStorage(t *testing.T) {
	s := NewMemStorage()
	s.SetGauge("temp", 36.6)
	s.AddCounter("hits", 527)

	snap := s.Snapshot()
	snap.Gauges["temp"] = -1
	snap.Gauges["injected"] = 1
	snap.Counters["hits"] = -1

	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": 36.6},
		Counters: map[string]metrics.Counter{"hits": 527},
	}, s.Snapshot())
}

func TestMemStorage_Snapshot_LaterWritesLeaveSnapshot(t *testing.T) {
	s := NewMemStorage()
	s.SetGauge("temp", 36.6)
	s.AddCounter("hits", 527)

	snap := s.Snapshot()
	s.SetGauge("temp", -1)
	s.SetGauge("added_later", 1)
	s.AddCounter("hits", 1)

	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": 36.6},
		Counters: map[string]metrics.Counter{"hits": 527},
	}, snap)
}
