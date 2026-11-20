// Package agent collects runtime metrics and reports them to the server.
package agent

import (
	"maps"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

// Snapshot carries a PollCount delta, not a total, because the server adds counters.
type Snapshot struct {
	Gauges    map[string]metrics.Gauge
	PollCount metrics.Counter
	// Taken orders batches on the server; a zero Taken sends the batch unstamped.
	Taken time.Time
}

type Collector struct {
	mu        sync.Mutex
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

	c.mu.Lock()
	defer c.mu.Unlock()

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

// Snapshot takes the PollCount delta away, so each poll is in exactly one snapshot however many
// reports are in flight.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap := Snapshot{Gauges: maps.Clone(c.gauges), PollCount: c.pollCount}
	c.pollCount = 0
	return snap
}

// Restore adds back the delta of a snapshot that was not delivered; the gauges stay, since the
// collector's own are newer.
func (c *Collector) Restore(s Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pollCount += s.PollCount
}
