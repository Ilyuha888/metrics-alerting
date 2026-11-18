package agent_test

import (
	"fmt"
	"maps"
	"runtime"
	"slices"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/agent"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemCollector_Poll_ReadsMemoryAndEveryCPU(t *testing.T) {
	s := agent.NewSystemCollector()

	require.NoError(t, s.Poll(t.Context()))
	got := s.Gauges()

	want := []string{"TotalMemory", "FreeMemory"}
	for i := 1; i <= runtime.NumCPU(); i++ {
		want = append(want, fmt.Sprintf("CPUutilization%d", i))
	}
	assert.ElementsMatch(t, want, slices.Collect(maps.Keys(got)))
	assert.Greater(t, got["TotalMemory"], metrics.Gauge(0))
	assert.Greater(t, got["FreeMemory"], metrics.Gauge(0))
	assert.LessOrEqual(t, got["FreeMemory"], got["TotalMemory"])
	for i := 1; i <= runtime.NumCPU(); i++ {
		load := got[fmt.Sprintf("CPUutilization%d", i)]
		assert.GreaterOrEqual(t, load, metrics.Gauge(0))
		assert.LessOrEqual(t, load, metrics.Gauge(100))
	}
}

func TestSystemCollector_Gauges_EmptyBeforeThePoll(t *testing.T) {
	assert.Empty(t, agent.NewSystemCollector().Gauges())
}

func TestSystemCollector_Gauges_ReturnsACopy(t *testing.T) {
	s := agent.NewSystemCollector()
	require.NoError(t, s.Poll(t.Context()))

	s.Gauges()["TotalMemory"] = -1

	assert.Greater(t, s.Gauges()["TotalMemory"], metrics.Gauge(0))
}
