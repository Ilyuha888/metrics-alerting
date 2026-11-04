// Package retry repeats an operation that failed with a temporary error, with pauses in between.
package retry

import (
	"context"
	"fmt"
	"time"
)

// DefaultDelays are the pauses between attempts: three more after the first.
var DefaultDelays = []time.Duration{time.Second, 3 * time.Second, 5 * time.Second}

// Do makes one attempt plus one per delay and returns as soon as an attempt succeeds. A
// non-retriable error comes back unchanged, so a caller's errors.Is and its text stay as they were.
func Do(ctx context.Context, delays []time.Duration, isRetriable func(error) bool, op func() error) error {
	for attempt := 1; ; attempt++ {
		err := op()
		if err == nil || !isRetriable(err) {
			return err
		}
		if attempt > len(delays) {
			return fmt.Errorf("after %d attempts: %w", attempt, err)
		}
		if ctxErr := pause(ctx, delays[attempt-1]); ctxErr != nil {
			return fmt.Errorf("stopped after attempt %d: %w: %w", attempt, ctxErr, err)
		}
	}
}

func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	// Not only for the first case: select picks at random when both are ready, and the next attempt
	// must not start on a context that is already done.
	return ctx.Err()
}
