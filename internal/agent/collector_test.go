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

func TestCollector_Reported_SubtractsDelivered(t *testing.T) {
	c := NewCollector()
	for range 5 {
		c.Poll()
	}
	delivered := c.Snapshot()
	require.Equal(t, metrics.Counter(5), delivered.PollCount)

	c.Poll() // a tick between the snapshot and the acknowledgement
	c.Reported(delivered)

	assert.Equal(t, metrics.Counter(1), c.Snapshot().PollCount)
}

func TestCollector_Reported_NotCalledKeepsPolls(t *testing.T) {
	c := NewCollector()
	c.Poll()
	_ = c.Snapshot() // report failed, nothing acknowledged
	c.Poll()

	assert.Equal(t, metrics.Counter(2), c.Snapshot().PollCount)
}
