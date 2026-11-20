package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

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
		if stamped {
			b.Stamp = h.trustedStamp(r, stamp)
		}
		if err := h.storage.UpdateBatch(r.Context(), b); err != nil {
			h.writeError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// maxStampLead bounds how far a stamp may run ahead of the server clock: a storage keeps the largest
// stamp each gauge saw, so one far in the future would skip every later batch's gauges.
const maxStampLead = time.Minute

// trustedStamp returns the stamp the batch is stored with, 0 for none. With a key, only a signed
// request may set a stamp, since an unsigned one passes WithSignature only for the autotests. A stamp
// past the limit is clamped, not dropped, so the order check stays on for an agent whose clock runs
// ahead, and a forged stamp holds a gauge for at most maxStampLead. Both cases log when they start
// and when they end, not per batch: a skewed or keyless agent repeats them on every report.
func (h *handlers) trustedStamp(r *http.Request, stamp int64) int64 {
	if h.keyed && !verified(r) {
		if h.unsigned.CompareAndSwap(false, true) {
			h.log.Warn("stamps ignored: request is not signed", "stamp", stamp)
		}
		return 0
	}
	if h.unsigned.CompareAndSwap(true, false) {
		h.log.Info("stamps honoured again: request is signed", "stamp", stamp)
	}
	if limit := h.now().Add(maxStampLead).UnixNano(); stamp > limit {
		if h.clamping.CompareAndSwap(false, true) {
			h.log.Warn("stamps clamped: ahead of the server clock", "stamp", stamp, "limit", limit)
		}
		return limit
	}
	if h.clamping.CompareAndSwap(true, false) {
		h.log.Info("stamps no longer clamped: within the server clock limit", "stamp", stamp)
	}
	return stamp
}

// batchStamp reports ok false when the header is absent: such a batch is stored as before.
func batchStamp(r *http.Request) (stamp int64, ok bool, err error) {
	v := r.Header.Get(models.TimestampHeader)
	if v == "" {
		return 0, false, nil
	}
	stamp, err = strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid %s: want Unix nanoseconds: %w", models.TimestampHeader, err)
	}
	return stamp, true, nil
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
