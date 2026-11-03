// External test package: everything goes through the Storage methods, as for a real caller.
package pgstore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/database"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/pgstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDatabase creates a throwaway database, so tests never add tables to the one
// TEST_DATABASE_DSN names and always start with no rows. The DSN must be in URL form: its
// path is replaced. The admin pool is a plain sql.Open, since database.Open would migrate it.
func newDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	target, err := url.Parse(dsn)
	require.NoError(t, err)

	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, admin.Close()) })

	name := fmt.Sprintf("test_%s", strings.ToLower(rand.Text()))
	_, err = admin.ExecContext(context.Background(), fmt.Sprintf("CREATE DATABASE %s", name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name))
		assert.NoError(t, err)
	})

	target.Path = name
	db, err := database.Open(context.Background(), target.String())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	return db
}

func TestStorage_SetGauge_LastReportWins(t *testing.T) {
	tests := []struct {
		name    string
		reports []metrics.Gauge
		want    metrics.Gauge
	}{
		{name: "first report is stored", reports: []metrics.Gauge{36.6}, want: 36.6},
		{name: "later report replaces", reports: []metrics.Gauge{36.6, 37.2}, want: 37.2},
		{name: "zero replaces", reports: []metrics.Gauge{36.6, 0}, want: 0},
		{name: "negative is allowed", reports: []metrics.Gauge{-1.5}, want: -1.5},
		{name: "float64 precision survives", reports: []metrics.Gauge{0.12345678901234568}, want: 0.12345678901234568},
	}
	s := pgstore.New(newDatabase(t))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range tt.reports {
				require.NoError(t, s.SetGauge(t.Context(), tt.name, v))
			}

			got, err := s.Gauge(t.Context(), tt.name)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStorage_AddCounter_ReportsAccumulate(t *testing.T) {
	tests := []struct {
		name    string
		reports []metrics.Counter
		want    metrics.Counter
	}{
		{name: "unknown name starts from zero", reports: []metrics.Counter{527}, want: 527},
		{name: "reports accumulate", reports: []metrics.Counter{1, 5}, want: 6},
		{name: "zero keeps the value", reports: []metrics.Counter{527, 0}, want: 527},
		{name: "negative subtracts", reports: []metrics.Counter{527, -27}, want: 500},
	}
	s := pgstore.New(newDatabase(t))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range tt.reports {
				require.NoError(t, s.AddCounter(t.Context(), tt.name, v))
			}

			got, err := s.Counter(t.Context(), tt.name)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStorage_Read_UnknownNameIsNotFound(t *testing.T) {
	s := pgstore.New(newDatabase(t))
	require.NoError(t, s.SetGauge(t.Context(), "only_gauge", 1))
	require.NoError(t, s.AddCounter(t.Context(), "only_counter", 1))

	tests := []struct {
		name string
		read func() error
	}{
		{name: "gauge never reported", read: func() error { _, err := s.Gauge(t.Context(), "nope"); return err }},
		{name: "counter never reported", read: func() error { _, err := s.Counter(t.Context(), "nope"); return err }},
		{name: "gauge lookup does not see counters", read: func() error { _, err := s.Gauge(t.Context(), "only_counter"); return err }},
		{name: "counter lookup does not see gauges", read: func() error { _, err := s.Counter(t.Context(), "only_gauge"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, tt.read(), metrics.ErrNotFound)
		})
	}
}

func TestStorage_SameNameAsBothKinds_StaysIndependent(t *testing.T) {
	s := pgstore.New(newDatabase(t))

	require.NoError(t, s.SetGauge(t.Context(), "same", 1.5))
	require.NoError(t, s.AddCounter(t.Context(), "same", 7))

	gauge, err := s.Gauge(t.Context(), "same")
	require.NoError(t, err)
	assert.Equal(t, metrics.Gauge(1.5), gauge, "a counter write leaves the gauge")

	require.NoError(t, s.SetGauge(t.Context(), "same", 2.5))

	counter, err := s.Counter(t.Context(), "same")
	require.NoError(t, err)
	assert.Equal(t, metrics.Counter(7), counter, "a gauge write leaves the counter")
}

func TestStorage_AddCounter_ParallelReportsAllCount(t *testing.T) {
	const writers, reports = 8, 200
	s := pgstore.New(newDatabase(t))

	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range reports {
				assert.NoError(t, s.AddCounter(t.Context(), "hits", 1))
			}
		})
	}
	wg.Wait()

	got, err := s.Counter(t.Context(), "hits")
	require.NoError(t, err)
	assert.Equal(t, metrics.Counter(writers*reports), got)
}

func TestStorage_Snapshot_HoldsBothKinds(t *testing.T) {
	s := pgstore.New(newDatabase(t))
	require.NoError(t, s.SetGauge(t.Context(), "temp", 36.6))
	require.NoError(t, s.SetGauge(t.Context(), "same", 1.5))
	require.NoError(t, s.AddCounter(t.Context(), "hits", 527))
	require.NoError(t, s.AddCounter(t.Context(), "same", 7))

	snap, err := s.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{"temp": 36.6, "same": 1.5},
		Counters: map[string]metrics.Counter{"hits": 527, "same": 7},
	}, snap)
}

func TestStorage_Snapshot_EmptyDatabaseGivesEmptyMaps(t *testing.T) {
	s := pgstore.New(newDatabase(t))

	snap, err := s.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Equal(t, metrics.Snapshot{Gauges: map[string]metrics.Gauge{}, Counters: map[string]metrics.Counter{}}, snap)
}

// The writer keeps both tables equal inside one transaction, so unequal values in a snapshot
// mean its two SELECTs saw two different moments.
func TestStorage_Snapshot_SeesOneMoment(t *testing.T) {
	const snapshots = 1000
	db := newDatabase(t)
	s := pgstore.New(db)
	require.NoError(t, s.SetGauge(t.Context(), "v", 0))
	require.NoError(t, s.AddCounter(t.Context(), "v", 0))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := db.Begin()
			if !assert.NoError(t, err) {
				return
			}
			_, err = tx.Exec(`UPDATE gauges SET value = $1 WHERE name = 'v'`, i)
			assert.NoError(t, err)
			_, err = tx.Exec(`UPDATE counters SET delta = $1 WHERE name = 'v'`, i)
			assert.NoError(t, err)
			assert.NoError(t, tx.Commit())
		}
	})

	for range snapshots {
		snap, err := s.Snapshot(t.Context())
		require.NoError(t, err)
		if int64(snap.Gauges["v"]) != int64(snap.Counters["v"]) {
			t.Errorf("gauge %v and counter %v come from different moments", snap.Gauges["v"], snap.Counters["v"])
			break
		}
	}
	close(stop)
	wg.Wait()
}

func TestStorage_Snapshot_TxOptionsAreRepeatableReadReadOnly(t *testing.T) {
	db := newDatabase(t)

	tx, err := db.BeginTx(t.Context(), pgstore.SnapshotTxOptions)
	require.NoError(t, err)
	defer tx.Rollback()

	var isolation, readOnly string
	require.NoError(t, tx.QueryRowContext(t.Context(), `SHOW transaction_isolation`).Scan(&isolation))
	require.NoError(t, tx.QueryRowContext(t.Context(), `SHOW transaction_read_only`).Scan(&readOnly))
	assert.Equal(t, "repeatable read", isolation)
	assert.Equal(t, "on", readOnly)

	_, err = tx.ExecContext(t.Context(), `INSERT INTO gauges (name, value) VALUES ('x', 1)`)
	assert.ErrorContains(t, err, "read-only transaction")
}

var everyMethod = []struct {
	name string
	call func(ctx context.Context, s *pgstore.Storage) error
}{
	{name: "SetGauge", call: func(ctx context.Context, s *pgstore.Storage) error { return s.SetGauge(ctx, "x", 1) }},
	{name: "AddCounter", call: func(ctx context.Context, s *pgstore.Storage) error { return s.AddCounter(ctx, "x", 1) }},
	{name: "Gauge", call: func(ctx context.Context, s *pgstore.Storage) error { _, err := s.Gauge(ctx, "x"); return err }},
	{name: "Counter", call: func(ctx context.Context, s *pgstore.Storage) error { _, err := s.Counter(ctx, "x"); return err }},
	{name: "Snapshot", call: func(ctx context.Context, s *pgstore.Storage) error { _, err := s.Snapshot(ctx); return err }},
}

func TestStorage_CancelledContext_ReturnsError(t *testing.T) {
	s := pgstore.New(newDatabase(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, tt := range everyMethod {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, tt.call(ctx, s), context.Canceled)
		})
	}
}

// An exclusive table lock blocks reads too, so all five methods wait on it until their own
// timeout ends. They wait in parallel, so the test takes one timeout, not five.
func TestStorage_StuckDatabase_TimesOutUnderOwnTimeout(t *testing.T) {
	db := newDatabase(t)
	s := pgstore.New(db)

	lock, err := db.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, lock.Rollback()) })
	_, err = lock.Exec(`LOCK TABLE gauges, counters IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	for _, tt := range everyMethod {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()

			err := tt.call(context.Background(), s)

			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.InDelta(t, 3*time.Second, time.Since(start), float64(time.Second))
		})
	}
}

func TestStorage_AddCounter_OverflowIsAnErrorAndKeepsTheValue(t *testing.T) {
	s := pgstore.New(newDatabase(t))
	require.NoError(t, s.AddCounter(t.Context(), "hits", math.MaxInt64))

	err := s.AddCounter(t.Context(), "hits", 1)

	assert.ErrorContains(t, err, "bigint out of range")
	assert.ErrorContains(t, err, `add counter "hits"`)
	got, err := s.Counter(t.Context(), "hits")
	require.NoError(t, err)
	assert.Equal(t, metrics.Counter(math.MaxInt64), got)
}
