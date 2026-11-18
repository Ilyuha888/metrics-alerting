package handler

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"

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
