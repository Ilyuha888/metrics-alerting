// Package pgstore keeps metrics in PostgreSQL.
package pgstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
)

// The HTTP server sets no timeouts, so without this a stuck database would hold a handler forever.
const queryTimeout = 3 * time.Second

// By default each statement sees its own moment, so a write landing between the two SELECTs would
// give gauges from before it and counters from after. Repeatable read gives both the same moment.
var snapshotTxOptions = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}

type Storage struct {
	db *sql.DB
}

func New(db *sql.DB) *Storage {
	return &Storage{db: db}
}

func (s *Storage) SetGauge(ctx context.Context, name string, v metrics.Gauge) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO gauges (name, value) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value`,
		name, v)
	if err != nil {
		return fmt.Errorf("set gauge %q: %w", name, err)
	}
	return nil
}

func (s *Storage) AddCounter(ctx context.Context, name string, v metrics.Counter) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	// The sum happens inside the statement, so parallel reports cannot overwrite each other.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO counters (name, delta) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET delta = counters.delta + EXCLUDED.delta`,
		name, v)
	if err != nil {
		return fmt.Errorf("add counter %q: %w", name, err)
	}
	return nil
}

func (s *Storage) Gauge(ctx context.Context, name string) (metrics.Gauge, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var v metrics.Gauge
	err := s.db.QueryRowContext(ctx, `SELECT value FROM gauges WHERE name = $1`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, metrics.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read gauge %q: %w", name, err)
	}
	return v, nil
}

func (s *Storage) Counter(ctx context.Context, name string) (metrics.Counter, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var v metrics.Counter
	err := s.db.QueryRowContext(ctx, `SELECT delta FROM counters WHERE name = $1`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, metrics.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read counter %q: %w", name, err)
	}
	return v, nil
}

func (s *Storage) Snapshot(ctx context.Context) (metrics.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, snapshotTxOptions)
	if err != nil {
		return metrics.Snapshot{}, fmt.Errorf("begin snapshot: %w", err)
	}
	// Read-only, so there is nothing a commit would keep.
	defer tx.Rollback()

	gauges, err := readAll[metrics.Gauge](ctx, tx, `SELECT name, value FROM gauges`)
	if err != nil {
		return metrics.Snapshot{}, fmt.Errorf("snapshot gauges: %w", err)
	}
	counters, err := readAll[metrics.Counter](ctx, tx, `SELECT name, delta FROM counters`)
	if err != nil {
		return metrics.Snapshot{}, fmt.Errorf("snapshot counters: %w", err)
	}
	return metrics.Snapshot{Gauges: gauges, Counters: counters}, nil
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
