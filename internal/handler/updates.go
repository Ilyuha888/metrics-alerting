package handler

import (
	"net/http"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

// maxBatchBody counts decompressed bytes: WithGzip replaces r.Body before any handler runs.
const maxBatchBody = 1 << 20

func (h *handlers) updates(w http.ResponseWriter, r *http.Request) {
	stamp, stamped, err := batchStamp(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBatchBody)

	var list []models.Metrics
	if !decodeJSON(w, r, &list) {
		return
	}

	// Checked before any write, so one bad element leaves the store as it was.
	for _, m := range list {
		if err := validateUpdate(m); err != nil {
			h.writeError(w, err)
			return
		}
	}

	if len(list) > 0 {
		b := mergeBatch(list)
		if stamped && h.trustStamp(r, stamp) {
			err = h.stamps.update(r.Context(), h.storage, b, stamp)
		} else {
			err = h.storage.UpdateBatch(r.Context(), b)
		}
		if err != nil {
			h.writeError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// mergeBatch expects validated elements. Postgres refuses one upsert touching a row twice, so
// repeated names must be merged before storage. A sum of two huge deltas wraps silently here.
func mergeBatch(list []models.Metrics) metrics.Batch {
	b := metrics.Batch{
		Gauges:   make(map[string]metrics.Gauge),
		Counters: make(map[string]metrics.Counter),
	}
	for _, m := range list {
		switch m.MType {
		case metrics.KindGauge:
			b.Gauges[m.ID] = metrics.Gauge(*m.Value)
		case metrics.KindCounter:
			b.Counters[m.ID] += metrics.Counter(*m.Delta)
		}
	}
	return b
}
