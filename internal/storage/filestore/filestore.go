// Package filestore persists a memstorage.Storage to a JSON file.
package filestore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/models"
	"github.com/Ilyuha888/metrics-alerting/internal/storage/memstorage"
)

type Storage struct {
	// Embedded so Gauge, Counter and Snapshot come from memstorage.Storage unchanged; only
	// SetGauge, AddCounter and UpdateBatch are redefined, to save and return its error. A
	// pointer, because a copy would carry its own mutex over the same maps.
	*memstorage.Storage
	path     string
	interval time.Duration
	log      *slog.Logger
	// With -i 0 every update saves, and net/http serves requests in parallel, so two
	// saves can overlap and rename an older snapshot over a newer one.
	saveMu sync.Mutex
	// A field only so a test can count saves: two saves of one batch leave the same file.
	syncSave func() error
}

// New creates the file's parent directory, so a first start with a nested path works.
// With restore it also loads the file, so the caller gets a storage that is ready to serve.
func New(mem *memstorage.Storage, path string, interval time.Duration, restore bool, lg *slog.Logger) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create dir for %s: %w", path, err)
	}
	s := &Storage{Storage: mem, path: path, interval: interval, log: lg}
	s.syncSave = s.Save
	if restore {
		if err := s.load(); err != nil {
			return nil, fmt.Errorf("restore metrics: %w", err)
		}
	}
	return s, nil
}

// load treats a missing file as a first start and returns nil.
func (s *Storage) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}

	var list []models.Metrics
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}

	// Writes go to the embedded memstorage.Storage, not s, so a restore does not save the file it reads.
	for _, m := range list {
		switch m.MType {
		case metrics.KindGauge:
			if m.Value == nil {
				return fmt.Errorf("%s: gauge %q has no value", s.path, m.ID)
			}
			if err := s.Storage.SetGauge(context.Background(), m.ID, metrics.Gauge(*m.Value)); err != nil {
				return fmt.Errorf("%s: gauge %q: %w", s.path, m.ID, err)
			}
		case metrics.KindCounter:
			if m.Delta == nil {
				return fmt.Errorf("%s: counter %q has no delta", s.path, m.ID)
			}
			if err := s.Storage.AddCounter(context.Background(), m.ID, metrics.Counter(*m.Delta)); err != nil {
				return fmt.Errorf("%s: counter %q: %w", s.path, m.ID, err)
			}
		default:
			return fmt.Errorf("%s: metric %q has unknown type %q", s.path, m.ID, m.MType)
		}
	}
	return nil
}

// Save writes a temp file and renames it over the old one, so a crash mid-write
// leaves the previous file intact instead of a truncated one.
func (s *Storage) Save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	snap, err := s.Snapshot(context.Background())
	if err != nil {
		return fmt.Errorf("snapshot metrics: %w", err)
	}
	data, err := json.MarshalIndent(toModels(snap), "", "  ")
	if err != nil {
		return fmt.Errorf("encode metrics: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), fmt.Sprintf("%s.*.tmp", filepath.Base(s.path)))
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if err := writeAndRename(tmp, data, s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func writeAndRename(tmp *os.File, data []byte, path string) error {
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}

// toModels sorts by type, then id, so an unchanged storage writes an identical file.
func toModels(snap metrics.Snapshot) []models.Metrics {
	list := make([]models.Metrics, 0, len(snap.Gauges)+len(snap.Counters))
	for id, v := range snap.Gauges {
		f := float64(v)
		list = append(list, models.Metrics{ID: id, MType: metrics.KindGauge, Value: &f})
	}
	for id, v := range snap.Counters {
		d := int64(v)
		list = append(list, models.Metrics{ID: id, MType: metrics.KindCounter, Delta: &d})
	}
	slices.SortFunc(list, func(a, b models.Metrics) int {
		return cmp.Or(cmp.Compare(a.MType, b.MType), cmp.Compare(a.ID, b.ID))
	})
	return list
}

func (s *Storage) SetGauge(ctx context.Context, name string, v metrics.Gauge) error {
	if err := s.Storage.SetGauge(ctx, name, v); err != nil {
		return err
	}
	return s.saveIfSync()
}

func (s *Storage) AddCounter(ctx context.Context, name string, v metrics.Counter) error {
	if err := s.Storage.AddCounter(ctx, name, v); err != nil {
		return err
	}
	return s.saveIfSync()
}

// UpdateBatch must stay redefined: the embedded method alone would never save the file.
func (s *Storage) UpdateBatch(ctx context.Context, b metrics.Batch) error {
	if err := s.Storage.UpdateBatch(ctx, b); err != nil {
		return err
	}
	return s.saveIfSync()
}

// saveIfSync returns a failed save without logging it, since the handler logs the error it
// answers 500 with. The value stays in memory either way, so a client retry adds a counter twice.
func (s *Storage) saveIfSync() error {
	if s.interval != 0 {
		return nil
	}
	return s.syncSave()
}

// SaveEvery never returns; run it in its own goroutine.
func (s *Storage) SaveEvery() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for range ticker.C {
		if err := s.Save(); err != nil {
			s.log.Error("save metrics", "err", err)
		}
	}
}
