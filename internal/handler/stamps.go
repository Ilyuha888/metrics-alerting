package handler

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
)

// stampGuard keeps, for each gauge, the stamp of the batch that last wrote it. Stamps live in memory
// only, so a restarted server takes the first stamped batch as is.
type stampGuard struct {
	mu   sync.Mutex
	last map[string]int64
}

func newStampGuard() *stampGuard {
	return &stampGuard{last: make(map[string]int64)}
}

// update holds the lock through the write: two batches that both passed the check outside it could
// land in either order, and the older gauge would win.
func (g *stampGuard) update(ctx context.Context, s Storage, b metrics.Batch, stamp int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	maps.DeleteFunc(b.Gauges, func(name string, _ metrics.Gauge) bool {
		last, ok := g.last[name]
		return ok && stamp <= last
	})
	if len(b.Gauges) == 0 && len(b.Counters) == 0 {
		return nil
	}
	if err := s.UpdateBatch(ctx, b); err != nil {
		return err
	}
	for name := range b.Gauges {
		g.last[name] = stamp
	}
	return nil
}

// maxStampLead bounds how far a stamp may run ahead of the server clock: the guard keeps the largest
// stamp it saw, so one far in the future would skip every later batch's gauges until restart.
const maxStampLead = time.Minute

// trustStamp tells whether the batch goes through the guard. An untrusted stamp is not an error: the
// batch is stored as unstamped. With a key, only a signed request may set a stamp, since an unsigned
// one passes WithSignature only for the autotests.
func (h *handlers) trustStamp(r *http.Request, stamp int64) bool {
	if h.keyed && !verified(r) {
		h.log.Warn("stamp ignored: request is not signed", "stamp", stamp)
		return false
	}
	if limit := h.now().Add(maxStampLead).UnixNano(); stamp > limit {
		h.log.Warn("stamp ignored: ahead of the server clock", "stamp", stamp, "limit", limit)
		return false
	}
	return true
}

// batchStamp reports ok false when the header is absent: such a batch is stored as before.
func batchStamp(r *http.Request) (stamp int64, ok bool, err error) {
	v := r.Header.Get(models.TimestampHeader)
	if v == "" {
		return 0, false, nil
	}
	stamp, err = strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid %s: want Unix nanoseconds", models.TimestampHeader)
	}
	return stamp, true, nil
}
