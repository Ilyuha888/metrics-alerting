package agent

import (
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spelled out, not derived from Poll: a dropped metric must fail here, not vanish twice.
var wantGauges = []string{
	"Alloc", "BuckHashSys", "Frees", "GCCPUFraction", "GCSys", "HeapAlloc", "HeapIdle",
	"HeapInuse", "HeapObjects", "HeapReleased", "HeapSys", "LastGC", "Lookups",
	"MCacheInuse", "MCacheSys", "MSpanInuse", "MSpanSys", "Mallocs", "NextGC",
	"NumForcedGC", "NumGC", "OtherSys", "PauseTotalNs", "StackInuse", "StackSys", "Sys",
	"TotalAlloc", "RandomValue",
}

func TestCollector_Poll_CollectsEveryMetric(t *testing.T) {
	c := NewCollector()
	c.Poll()

	got := c.Snapshot()

	names := make([]string, 0, len(got.Gauges))
	for name := range got.Gauges {
		names = append(names, name)
	}
	assert.ElementsMatch(t, wantGauges, names)
	assert.Equal(t, metrics.Counter(1), got.PollCount)
}

func TestCollector_Poll_CountsEveryTick(t *testing.T) {
	c := NewCollector()
	for range 3 {
		c.Poll()
	}

	assert.Equal(t, metrics.Counter(3), c.Snapshot().PollCount)
}

func TestCollector_Snapshot_EditingSnapshotLeavesCollector(t *testing.T) {
	c := NewCollector()
	c.Poll()
	before := c.gauges["Alloc"]

	snap := c.Snapshot()
	snap.Gauges["Alloc"] = -1

	assert.Equal(t, before, c.gauges["Alloc"])
}

func TestCollector_Snapshot_LaterPollsLeaveSnapshot(t *testing.T) {
	c := NewCollector()
	c.Poll()
	snap := c.Snapshot()
	taken := snap.Gauges["Alloc"]

	c.gauges["Alloc"] = -1

	assert.Equal(t, taken, snap.Gauges["Alloc"])
}

func TestCollector_Snapshot_TakesThePollCount(t *testing.T) {
	c := NewCollector()
	for range 5 {
		c.Poll()
	}

	first := c.Snapshot()
	c.Poll()
	second := c.Snapshot()

	assert.Equal(t, metrics.Counter(5), first.PollCount)
	assert.Equal(t, metrics.Counter(1), second.PollCount, "a poll goes into one snapshot only")
	assert.Equal(t, metrics.Counter(0), c.Snapshot().PollCount)
}

func TestCollector_Restore_AddsTheDeltaBack(t *testing.T) {
	c := NewCollector()
	for range 5 {
		c.Poll()
	}
	failed := c.Snapshot()
	require.Equal(t, metrics.Counter(5), failed.PollCount)

	c.Poll()
	c.Restore(failed)

	assert.Equal(t, metrics.Counter(6), c.Snapshot().PollCount)
}
