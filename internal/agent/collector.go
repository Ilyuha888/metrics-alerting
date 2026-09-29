// Package agent collects runtime metrics and reports them to the server.
package agent

import (
	"maps"
	"math/rand/v2"
	"runtime"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

// Snapshot carries a PollCount delta, not a total, because the server adds counters.
type Snapshot struct {
	Gauges    map[string]metrics.Gauge
	PollCount metrics.Counter
}

type Collector struct {
	gauges    map[string]metrics.Gauge
	pollCount metrics.Counter
}

func NewCollector() *Collector {
	return &Collector{gauges: make(map[string]metrics.Gauge, 28)}
}

// Poll refreshes everything at once, so a caller never sees half a reading.
func (c *Collector) Poll() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	c.gauges["Alloc"] = metrics.Gauge(m.Alloc)
	c.gauges["BuckHashSys"] = metrics.Gauge(m.BuckHashSys)
	c.gauges["Frees"] = metrics.Gauge(m.Frees)
	c.gauges["GCCPUFraction"] = metrics.Gauge(m.GCCPUFraction)
	c.gauges["GCSys"] = metrics.Gauge(m.GCSys)
	c.gauges["HeapAlloc"] = metrics.Gauge(m.HeapAlloc)
	c.gauges["HeapIdle"] = metrics.Gauge(m.HeapIdle)
	c.gauges["HeapInuse"] = metrics.Gauge(m.HeapInuse)
	c.gauges["HeapObjects"] = metrics.Gauge(m.HeapObjects)
	c.gauges["HeapReleased"] = metrics.Gauge(m.HeapReleased)
	c.gauges["HeapSys"] = metrics.Gauge(m.HeapSys)
	c.gauges["LastGC"] = metrics.Gauge(m.LastGC)
	c.gauges["Lookups"] = metrics.Gauge(m.Lookups)
	c.gauges["MCacheInuse"] = metrics.Gauge(m.MCacheInuse)
	c.gauges["MCacheSys"] = metrics.Gauge(m.MCacheSys)
	c.gauges["MSpanInuse"] = metrics.Gauge(m.MSpanInuse)
	c.gauges["MSpanSys"] = metrics.Gauge(m.MSpanSys)
	c.gauges["Mallocs"] = metrics.Gauge(m.Mallocs)
	c.gauges["NextGC"] = metrics.Gauge(m.NextGC)
	c.gauges["NumForcedGC"] = metrics.Gauge(m.NumForcedGC)
	c.gauges["NumGC"] = metrics.Gauge(m.NumGC)
	c.gauges["OtherSys"] = metrics.Gauge(m.OtherSys)
	c.gauges["PauseTotalNs"] = metrics.Gauge(m.PauseTotalNs)
	c.gauges["StackInuse"] = metrics.Gauge(m.StackInuse)
	c.gauges["StackSys"] = metrics.Gauge(m.StackSys)
	c.gauges["Sys"] = metrics.Gauge(m.Sys)
	c.gauges["TotalAlloc"] = metrics.Gauge(m.TotalAlloc)
	c.gauges["RandomValue"] = metrics.Gauge(rand.Float64())

	c.pollCount++
}

// Snapshot copies and mutates nothing, so a failed report loses no polls.
func (c *Collector) Snapshot() Snapshot {
	return Snapshot{Gauges: maps.Clone(c.gauges), PollCount: c.pollCount}
}

// Reported subtracts what was delivered rather than zeroing, which stays correct when a
// poll lands between the snapshot and this call.
func (c *Collector) Reported(s Snapshot) {
	c.pollCount -= s.PollCount
}
