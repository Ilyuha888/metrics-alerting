// Package metrics is the vocabulary of the whole service: metric kinds, value types,
// snapshots and errors. Storage and handler both import it, so neither imports the other.
package metrics

type Kind string

const (
	KindGauge   Kind = "gauge"
	KindCounter Kind = "counter"
)

// Gauge is a sampled value: a new report replaces the previous one.
type Gauge float64

// Counter is an accumulating value: a new report is added to the previous one.
type Counter int64

// Snapshot is every known metric at one moment.
type Snapshot struct {
	Gauges   map[string]Gauge
	Counters map[string]Counter
}

// Batch is one report: a gauge name appears once, a counter holds the sum of its deltas.
type Batch struct {
	Gauges   map[string]Gauge
	Counters map[string]Counter
	// Stamp orders batches that reach the server out of order: a storage writes a gauge only if the
	// stamp is newer than the one stored with it. 0 means no stamp, and the gauges are written as is.
	Stamp int64
}
