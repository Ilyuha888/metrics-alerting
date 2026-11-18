package agent

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// SystemCollector reads memory and CPU load of the host through gopsutil.
type SystemCollector struct {
	mu     sync.Mutex
	gauges map[string]metrics.Gauge
}

func NewSystemCollector() *SystemCollector {
	return &SystemCollector{gauges: make(map[string]metrics.Gauge)}
}

// Poll replaces every gauge at once or, on an error, none. CPU load is measured against the
// previous call rather than over a pause, so it covers the whole poll interval and Poll never
// sleeps; the first call compares with the moment gopsutil was loaded.
func (s *SystemCollector) Poll(ctx context.Context) error {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return fmt.Errorf("read memory: %w", err)
	}
	load, err := cpu.PercentWithContext(ctx, 0, true)
	if err != nil {
		return fmt.Errorf("read cpu utilization: %w", err)
	}

	gauges := make(map[string]metrics.Gauge, 2+len(load))
	gauges["TotalMemory"] = metrics.Gauge(vm.Total)
	gauges["FreeMemory"] = metrics.Gauge(vm.Free)
	for i, percent := range load {
		gauges[fmt.Sprintf("CPUutilization%d", i+1)] = metrics.Gauge(percent)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.gauges = gauges
	return nil
}

func (s *SystemCollector) Gauges() map[string]metrics.Gauge {
	s.mu.Lock()
	defer s.mu.Unlock()

	return maps.Clone(s.gauges)
}
