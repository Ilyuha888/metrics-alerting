// Package models is the JSON wire format shared by agent and server.
package models

import "github.com/Ilyuha888/metrics-alerting/internal/metrics"

// TimestampHeader carries when the agent took a batch, in Unix nanoseconds, so the server can drop
// gauges older than the ones it holds when batches arrive out of order.
const TimestampHeader = "X-Metrics-Timestamp"

// Delta and Value are pointers so an absent field stays nil instead of reading as zero.
type Metrics struct {
	ID    string       `json:"id"`
	MType metrics.Kind `json:"type"`
	Delta *int64       `json:"delta,omitempty"`
	Value *float64     `json:"value,omitempty"`
}
