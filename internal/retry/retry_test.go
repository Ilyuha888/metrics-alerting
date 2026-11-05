package retry_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errTemporary = errors.New("temporary")

func isTemporary(err error) bool { return errors.Is(err, errTemporary) }

// attempts plays back results in order, then keeps returning the last one, and records how long
// after the start each call came.
type attempts struct {
	results []error
	start   time.Time
	at      []time.Duration
}

func (a *attempts) op() error {
	a.at = append(a.at, time.Since(a.start))
	return a.results[min(len(a.at), len(a.results))-1]
}

func TestDo_RepeatsOnlyWhatIsRetriable(t *testing.T) {
	errPermanent := errors.New("permanent")
	tests := []struct {
		name    string
		results []error
		delays  []time.Duration
		wantAt  []time.Duration
		wantErr error
	}{
		{
			name:    "success first time makes one call and no pause",
			results: []error{nil},
			delays:  retry.DefaultDelays,
			wantAt:  []time.Duration{0},
		},
		{
			name:    "retriable all the way pauses 1s 3s 5s",
			results: []error{errTemporary},
			delays:  retry.DefaultDelays,
			wantAt:  []time.Duration{0, time.Second, 4 * time.Second, 9 * time.Second},
			wantErr: errTemporary,
		},
		{
			name:    "success on the third call stops there",
			results: []error{errTemporary, errTemporary, nil},
			delays:  retry.DefaultDelays,
			wantAt:  []time.Duration{0, time.Second, 4 * time.Second},
		},
		{
			name:    "non-retriable after a retriable stops there",
			results: []error{errTemporary, errPermanent},
			delays:  retry.DefaultDelays,
			wantAt:  []time.Duration{0, time.Second},
			wantErr: errPermanent,
		},
		{
			name:    "empty delays make one call",
			results: []error{errTemporary},
			delays:  nil,
			wantAt:  []time.Duration{0},
			wantErr: errTemporary,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := &attempts{results: tt.results, start: time.Now()}

				err := retry.Do(t.Context(), tt.delays, isTemporary, a.op)

				assert.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, tt.wantAt, a.at)
			})
		})
	}
}

func TestDo_NonRetriableReturnsTheErrorUnchanged(t *testing.T) {
	errPermanent := errors.New("permanent")
	calls := 0

	err := retry.Do(t.Context(), retry.DefaultDelays, isTemporary, func() error {
		calls++
		return errPermanent
	})

	assert.True(t, err == errPermanent, "the same value, with nothing wrapped around it")
	assert.Equal(t, 1, calls)
}

func TestDo_ExhaustedAttemptsNameTheirNumber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &attempts{results: []error{errTemporary}, start: time.Now()}

		err := retry.Do(t.Context(), retry.DefaultDelays, isTemporary, a.op)

		assert.ErrorContains(t, err, "after 4 attempts")
	})
}

type codeError struct{ code string }

func (e *codeError) Error() string { return e.code }

func TestDo_ExhaustedAttemptsKeepTheLastErrorForAs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		last := &codeError{code: "last"}
		results := []error{&codeError{code: "first"}, last}
		a := &attempts{results: results, start: time.Now()}

		err := retry.Do(t.Context(), []time.Duration{time.Second}, func(error) bool { return true }, a.op)

		var got *codeError
		require.ErrorAs(t, err, &got)
		assert.Same(t, last, got)
	})
}

func TestDo_CancelDuringAPauseStopsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		time.AfterFunc(500*time.Millisecond, cancel)
		a := &attempts{results: []error{errTemporary}, start: start}

		err := retry.Do(ctx, retry.DefaultDelays, isTemporary, a.op)

		assert.ErrorIs(t, err, context.Canceled)
		assert.ErrorIs(t, err, errTemporary)
		assert.Equal(t, 500*time.Millisecond, time.Since(start))
		assert.Equal(t, []time.Duration{0}, a.at)
	})
}

// A cancelled context and a zero pause leave select with both cases ready, and it picks at random.
// The repeats make that pick land on the timer.
func TestDo_ContextDoneAsThePauseEndsStartsNoAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 100 {
		calls := 0

		err := retry.Do(ctx, []time.Duration{0}, isTemporary, func() error {
			calls++
			return errTemporary
		})

		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, calls)
	}
}

func TestDo_AlreadyCancelledContextMakesTheFirstAttemptOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		a := &attempts{results: []error{errTemporary}, start: start}

		err := retry.Do(ctx, retry.DefaultDelays, isTemporary, a.op)

		assert.ErrorIs(t, err, context.Canceled)
		assert.ErrorIs(t, err, errTemporary)
		assert.Zero(t, time.Since(start))
		assert.Equal(t, []time.Duration{0}, a.at)
	})
}
