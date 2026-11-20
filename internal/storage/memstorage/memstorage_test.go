// Internal test package: Storage exposes no reads to assert through.
package memstorage

import (
	"fmt"
	"math/rand/v2"
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

func TestStorage_UpdateBatch_AppliesBothKinds(t *testing.T) {
	tests := []struct {
		name  string
		batch metrics.Batch
		want  metrics.Snapshot
	}{
		{
			name: "gauge replaces and counter adds to the stored value",
			batch: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": 37.2},
				Counters: map[string]metrics.Counter{"hits": 3},
			},
			want: metrics.Snapshot{
				Gauges:   map[string]metrics.Gauge{"temp": 37.2},
				Counters: map[string]metrics.Counter{"hits": 530},
			},
		},
		{
			name: "new names are added",
			batch: metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"load": 1.5},
				Counters: map[string]metrics.Counter{"errors": 2},
			},
			want: metrics.Snapshot{
				Gauges:   map[string]metrics.Gauge{"temp": 36.6, "load": 1.5},
				Counters: map[string]metrics.Counter{"hits": 527, "errors": 2},
			},
		},
		{
			name: "an empty batch leaves everything",
			want: metrics.Snapshot{
				Gauges:   map[string]metrics.Gauge{"temp": 36.6},
				Counters: map[string]metrics.Counter{"hits": 527},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			s.SetGauge(t.Context(), "temp", 36.6)
			s.AddCounter(t.Context(), "hits", 527)

			require.NoError(t, s.UpdateBatch(t.Context(), tt.batch))

			assert.Equal(t, tt.want, snapshot(t, s))
		})
	}
}

func TestStorage_UpdateBatch_SnapshotSeesAllOfABatchOrNone(t *testing.T) {
	const names, snapshots = 50, 2000
	s := New()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			b := metrics.Batch{Gauges: make(map[string]metrics.Gauge), Counters: make(map[string]metrics.Counter)}
			for n := range names {
				name := fmt.Sprintf("m%d", n)
				b.Gauges[name] = metrics.Gauge(i)
				b.Counters[name] = 1
			}
			assert.NoError(t, s.UpdateBatch(t.Context(), b))
		}
	})

	for range snapshots {
		snap := snapshot(t, s)
		if len(snap.Gauges) == 0 {
			continue
		}
		if !assert.Len(t, snap.Gauges, names) || !assert.Len(t, snap.Counters, names) {
			break
		}
		want := snap.Gauges["m0"]
		for n := range names {
			name := fmt.Sprintf("m%d", n)
			if snap.Gauges[name] != want || metrics.Gauge(snap.Counters[name]) != want {
				t.Errorf("%s: gauge %v, counter %v, batch %v", name, snap.Gauges[name], snap.Counters[name], want)
			}
		}
		if t.Failed() {
			break
		}
	}
	close(stop)
	wg.Wait()
}

type write func(t *testing.T, s *Storage)

func stamped(stamp int64, gauges map[string]metrics.Gauge, counters map[string]metrics.Counter) write {
	return func(t *testing.T, s *Storage) {
		require.NoError(t, s.UpdateBatch(t.Context(), metrics.Batch{Gauges: gauges, Counters: counters, Stamp: stamp}))
	}
}

func setGauge(name string, v metrics.Gauge) write {
	return func(t *testing.T, s *Storage) {
		require.NoError(t, s.SetGauge(t.Context(), name, v))
	}
}

func TestStorage_UpdateBatch_StampedGaugeKeepsTheNewest(t *testing.T) {
	tests := []struct {
		name       string
		writes     []write
		wantGauges map[string]metrics.Gauge
		wantHits   metrics.Counter
		wantStamps map[string]int64
	}{
		{
			name:       "newer stamp replaces the gauge",
			writes:     []write{stamped(100, map[string]metrics.Gauge{"temp": 1}, nil), stamped(200, map[string]metrics.Gauge{"temp": 2}, nil)},
			wantGauges: map[string]metrics.Gauge{"temp": 2},
			wantStamps: map[string]int64{"temp": 200},
		},
		{
			name: "older stamp skips the gauge, adds the counter and writes a gauge it is first for",
			writes: []write{
				stamped(200, map[string]metrics.Gauge{"temp": 2}, map[string]metrics.Counter{"hits": 1}),
				stamped(100, map[string]metrics.Gauge{"temp": 1, "fresh": 5}, map[string]metrics.Counter{"hits": 1}),
			},
			wantGauges: map[string]metrics.Gauge{"temp": 2, "fresh": 5},
			wantHits:   2,
			wantStamps: map[string]int64{"temp": 200, "fresh": 100},
		},
		{
			name:       "equal stamp skips the gauge",
			writes:     []write{stamped(200, map[string]metrics.Gauge{"temp": 2}, nil), stamped(200, map[string]metrics.Gauge{"temp": 3}, nil)},
			wantGauges: map[string]metrics.Gauge{"temp": 2},
			wantStamps: map[string]int64{"temp": 200},
		},
		{
			name:       "stamped batch writes a gauge only ever written unstamped",
			writes:     []write{stamped(0, map[string]metrics.Gauge{"temp": 9}, nil), setGauge("temp", 8), stamped(1, map[string]metrics.Gauge{"temp": 1}, nil)},
			wantGauges: map[string]metrics.Gauge{"temp": 1},
			wantStamps: map[string]int64{"temp": 1},
		},
		{
			name: "unstamped batch writes the value and keeps the stamp, so an older stamped one is still skipped",
			writes: []write{
				stamped(200, map[string]metrics.Gauge{"temp": 2}, nil),
				stamped(0, map[string]metrics.Gauge{"temp": 7}, map[string]metrics.Counter{"hits": 1}),
				stamped(100, map[string]metrics.Gauge{"temp": 1}, nil),
			},
			wantGauges: map[string]metrics.Gauge{"temp": 7},
			wantHits:   1,
			wantStamps: map[string]int64{"temp": 200},
		},
		{
			name:       "SetGauge writes the value and keeps the stamp",
			writes:     []write{stamped(200, map[string]metrics.Gauge{"temp": 2}, nil), setGauge("temp", 7), stamped(100, map[string]metrics.Gauge{"temp": 1}, nil)},
			wantGauges: map[string]metrics.Gauge{"temp": 7},
			wantStamps: map[string]int64{"temp": 200},
		},
		{
			name:       "stamps are per gauge",
			writes:     []write{stamped(200, map[string]metrics.Gauge{"temp": 2}, nil), stamped(100, map[string]metrics.Gauge{"load": 1}, nil)},
			wantGauges: map[string]metrics.Gauge{"temp": 2, "load": 1},
			wantStamps: map[string]int64{"temp": 200, "load": 100},
		},
		{
			name:       "older stamp of stale gauges only changes nothing",
			writes:     []write{stamped(200, map[string]metrics.Gauge{"temp": 2}, nil), stamped(100, map[string]metrics.Gauge{"temp": 1}, nil)},
			wantGauges: map[string]metrics.Gauge{"temp": 2},
			wantStamps: map[string]int64{"temp": 200},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()

			for _, w := range tt.writes {
				w(t, s)
			}

			snap, stamps := s.SnapshotWithStamps()
			wantCounters := map[string]metrics.Counter{}
			if tt.wantHits != 0 {
				wantCounters["hits"] = tt.wantHits
			}
			assert.Equal(t, metrics.Snapshot{Gauges: tt.wantGauges, Counters: wantCounters}, snap)
			assert.Equal(t, tt.wantStamps, stamps)
		})
	}
}

// Batches race with stamps in random order; a check made outside the lock would let an older one win.
func TestStorage_UpdateBatch_ConcurrentStampedBatchesKeepTheNewest(t *testing.T) {
	const batches = 1000
	s := New()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, n := range rand.Perm(batches) {
		wg.Go(func() {
			<-start
			stamp := int64(n + 1)
			assert.NoError(t, s.UpdateBatch(t.Context(), metrics.Batch{
				Gauges:   map[string]metrics.Gauge{"temp": metrics.Gauge(stamp)},
				Counters: map[string]metrics.Counter{"hits": 1},
				Stamp:    stamp,
			}))
		})
	}
	close(start)
	wg.Wait()

	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": batches},
		Counters: map[string]metrics.Counter{"hits": batches},
	}, snapshot(t, s))
}
