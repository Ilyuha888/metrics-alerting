// Internal test package: Storage exposes no reads to assert through.
package memstorage

import (
	"sync"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func snapshot(t *testing.T, s *Storage) metrics.Snapshot {
	t.Helper()
	snap, err := s.Snapshot(t.Context())
	require.NoError(t, err)
	return snap
}

func TestStorage_SetGauge(t *testing.T) {
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
			s := New()
			for _, v := range tt.reports {
				s.SetGauge(t.Context(), "temp", v)
			}
			assert.Equal(t, tt.want, s.gauges["temp"])
		})
	}
}

func TestStorage_AddCounter(t *testing.T) {
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
			s := New()
			for _, v := range tt.reports {
				s.AddCounter(t.Context(), "hits", v)
			}
			assert.Equal(t, tt.want, s.counters["hits"])
		})
	}
}

func TestStorage_NamesAndKindsAreIndependent(t *testing.T) {
	s := New()
	s.SetGauge(t.Context(), "same", 1.5)
	s.AddCounter(t.Context(), "same", 7)

	assert.Equal(t, metrics.Gauge(1.5), s.gauges["same"])
	assert.Equal(t, metrics.Counter(7), s.counters["same"])
}

func TestStorage_Gauge(t *testing.T) {
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
			s := New()
			for name, v := range tt.stored {
				s.SetGauge(t.Context(), name, v)
			}

			got, err := s.Gauge(t.Context(), tt.lookup)

			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStorage_Counter(t *testing.T) {
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
			s := New()
			for name, v := range tt.stored {
				s.AddCounter(t.Context(), name, v)
			}

			got, err := s.Counter(t.Context(), tt.lookup)

			assert.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStorage_Counter_DoesNotSeeGauges(t *testing.T) {
	s := New()
	s.SetGauge(t.Context(), "only_gauge", 1)

	_, err := s.Counter(t.Context(), "only_gauge")

	assert.ErrorIs(t, err, metrics.ErrNotFound)
}

func TestStorage_Snapshot_HoldsBothKinds(t *testing.T) {
	s := New()
	s.SetGauge(t.Context(), "temp", 36.6)
	s.AddCounter(t.Context(), "hits", 527)

	snap := snapshot(t, s)

	assert.Equal(t, map[string]metrics.Gauge{"temp": 36.6}, snap.Gauges)
	assert.Equal(t, map[string]metrics.Counter{"hits": 527}, snap.Counters)
}

// A map value is a reference: without a copy, the snapshot and the storage would be the
// same map and a change on either side would show on the other. Both directions are checked.
func TestStorage_Snapshot_EditingSnapshotLeavesStorage(t *testing.T) {
	s := New()
	s.SetGauge(t.Context(), "temp", 36.6)
	s.AddCounter(t.Context(), "hits", 527)

	snap := snapshot(t, s)
	snap.Gauges["temp"] = -1
	snap.Gauges["injected"] = 1
	snap.Counters["hits"] = -1

	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": 36.6},
		Counters: map[string]metrics.Counter{"hits": 527},
	}, snapshot(t, s))
}

func TestStorage_Snapshot_LaterWritesLeaveSnapshot(t *testing.T) {
	s := New()
	s.SetGauge(t.Context(), "temp", 36.6)
	s.AddCounter(t.Context(), "hits", 527)

	snap := snapshot(t, s)
	s.SetGauge(t.Context(), "temp", -1)
	s.SetGauge(t.Context(), "added_later", 1)
	s.AddCounter(t.Context(), "hits", 1)

	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": 36.6},
		Counters: map[string]metrics.Counter{"hits": 527},
	}, snap)
}

// Writers and a reader run at once; under -race an unguarded map fails this test.
func TestStorage_Snapshot_ConcurrentWithWritesIsSafe(t *testing.T) {
	const writers, reports = 8, 100
	s := New()

	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for i := range reports {
				s.SetGauge(t.Context(), "temp", metrics.Gauge(i))
				s.AddCounter(t.Context(), "hits", 1)
			}
		})
	}
	wg.Go(func() {
		for range reports {
			_, _ = s.Snapshot(t.Context())
		}
	})
	wg.Wait()

	assert.Equal(t, metrics.Counter(writers*reports), s.counters["hits"])
}
