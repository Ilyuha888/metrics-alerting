// Package metrics holds the vocabulary shared by the storage and the HTTP layer.
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
