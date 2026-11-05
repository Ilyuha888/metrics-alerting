// Package pgstore keeps metrics in PostgreSQL.
package pgstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/retry"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/database"
)

// The HTTP server sets no timeouts, so without this a stuck database would hold a handler forever.
const queryTimeout = 3 * time.Second

// By default each statement sees its own moment, so a write landing between the two SELECTs would
// give gauges from before it and counters from after. Repeatable read gives both the same moment.
var snapshotTxOptions = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}

// errBegin marks a batch that failed before its transaction started, so nothing of it was sent.
var errBegin = errors.New("begin")

type Storage struct {
	db     *sql.DB
	delays []time.Duration
}

func New(db *sql.DB) *Storage {
	return &Storage{db: db, delays: retry.DefaultDelays}
}

// withRetry gives each attempt its own timeout, and the pause waits on the caller's ctx: an attempt
// that used up its time must not end the repeats, while a caller that left must.
func (s *Storage) withRetry(ctx context.Context, isRetriable func(error) bool, attempt func(ctx context.Context) error) error {
	return retry.Do(ctx, s.delays, isRetriable, func() error {
		ctx, cancel := context.WithTimeout(ctx, queryTimeout)
		defer cancel()
		return attempt(ctx)
	})
}

// safeToRepeat serves the methods that cannot add a counter: a second write of a gauge stores the same
// value, and a read changes nothing.
func safeToRepeat(err error) bool {
	return database.IsRetriable(err) || database.IsConnectionLost(err)
}

// batchIsRetriable lets a lost connection repeat only before the transaction started: after that the
// lost answer can be the one to the commit, and a repeat would add the counters twice.
func batchIsRetriable(err error) bool {
	return database.IsRetriable(err) || (errors.Is(err, errBegin) && database.IsConnectionLost(err))
}

func (s *Storage) SetGauge(ctx context.Context, name string, v metrics.Gauge) error {
	return s.withRetry(ctx, safeToRepeat, func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO gauges (name, value) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value`,
			name, v)
		if err != nil {
			return fmt.Errorf("set gauge %q: %w", name, err)
		}
		return nil
	})
}

func (s *Storage) AddCounter(ctx context.Context, name string, v metrics.Counter) error {
	return s.withRetry(ctx, database.IsRetriable, func(ctx context.Context) error {
		// The sum happens inside the statement, so parallel reports cannot overwrite each other.
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO counters (name, delta) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET delta = counters.delta + EXCLUDED.delta`,
			name, v)
		if err != nil {
			return fmt.Errorf("add counter %q: %w", name, err)
		}
		return nil
	})
}

// UpdateBatch writes both tables in one transaction, always gauges first and names sorted: two
// transactions locking the same rows in different orders deadlock, and map order is random.
// A repeat starts from BeginTx, since a failed transaction cannot go on from the failed statement.
func (s *Storage) UpdateBatch(ctx context.Context, b metrics.Batch) error {
	return s.withRetry(ctx, batchIsRetriable, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("%w batch: %w", errBegin, err)
		}
		// After a commit this only returns ErrTxDone. On an early return it undoes the batch and keeps
		// the connection: left to cancel, the rollback closes the connection, and the next call reconnects.
		defer tx.Rollback()

		if err := upsertGauges(ctx, tx, b.Gauges); err != nil {
			return fmt.Errorf("batch gauges: %w", err)
		}
		if err := upsertCounters(ctx, tx, b.Counters); err != nil {
			return fmt.Errorf("batch counters: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit batch: %w", err)
		}
		return nil
	})
}

func upsertGauges(ctx context.Context, tx *sql.Tx, gauges map[string]metrics.Gauge) error {
	names := slices.Sorted(maps.Keys(gauges))
	values := make([]float64, len(names))
	for i, name := range names {
		values[i] = float64(gauges[name])
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO gauges (name, value) SELECT * FROM unnest($1::text[], $2::double precision[]) ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value`,
		names, values)
	return err
}

func upsertCounters(ctx context.Context, tx *sql.Tx, counters map[string]metrics.Counter) error {
	names := slices.Sorted(maps.Keys(counters))
	deltas := make([]int64, len(names))
	for i, name := range names {
		deltas[i] = int64(counters[name])
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO counters (name, delta) SELECT * FROM unnest($1::text[], $2::bigint[]) ON CONFLICT (name) DO UPDATE SET delta = counters.delta + EXCLUDED.delta`,
		names, deltas)
	return err
}

func (s *Storage) Gauge(ctx context.Context, name string) (metrics.Gauge, error) {
	var v metrics.Gauge
	err := s.withRetry(ctx, safeToRepeat, func(ctx context.Context) error {
		err := s.db.QueryRowContext(ctx, `SELECT value FROM gauges WHERE name = $1`, name).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return metrics.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read gauge %q: %w", name, err)
		}
		return nil
	})
	return v, err
}

func (s *Storage) Counter(ctx context.Context, name string) (metrics.Counter, error) {
	var v metrics.Counter
	err := s.withRetry(ctx, safeToRepeat, func(ctx context.Context) error {
		err := s.db.QueryRowContext(ctx, `SELECT delta FROM counters WHERE name = $1`, name).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return metrics.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read counter %q: %w", name, err)
		}
		return nil
	})
	return v, err
}

func (s *Storage) Snapshot(ctx context.Context) (metrics.Snapshot, error) {
	var snap metrics.Snapshot
	err := s.withRetry(ctx, safeToRepeat, func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, snapshotTxOptions)
		if err != nil {
			return fmt.Errorf("begin snapshot: %w", err)
		}
		// Read-only, so a commit would keep nothing. Left to cancel, the rollback closes the connection,
		// and the next call reconnects.
		defer tx.Rollback()

		gauges, err := readAll[metrics.Gauge](ctx, tx, `SELECT name, value FROM gauges`)
		if err != nil {
			return fmt.Errorf("snapshot gauges: %w", err)
		}
		counters, err := readAll[metrics.Counter](ctx, tx, `SELECT name, delta FROM counters`)
		if err != nil {
			return fmt.Errorf("snapshot counters: %w", err)
		}
		snap = metrics.Snapshot{Gauges: gauges, Counters: counters}
		return nil
	})
	return snap, err
}

func readAll[V metrics.Gauge | metrics.Counter](ctx context.Context, tx *sql.Tx, query string) (map[string]V, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	all := make(map[string]V)
	for rows.Next() {
		var name string
		var v V
		if err := rows.Scan(&name, &v); err != nil {
			return nil, err
		}
		all[name] = v
	}
	// Next also returns false on a lost connection; without this that reads as a short result.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return all, nil
}
