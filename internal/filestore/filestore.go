// Package filestore persists a MemStorage to a JSON file.
package filestore

import (
	"cmp"
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
	"github.com/Ilyuha888/metrics-alerting/internal/storage"
)

type Storage struct {
	// Embedded so Gauge, Counter and Snapshot come from MemStorage unchanged; only
	// SetGauge and AddCounter are redefined, to save and return its error. A pointer,
	// because a copy would carry its own mutex over the same maps.
	*storage.MemStorage
	path     string
	interval time.Duration
	log      *slog.Logger
	// With -i 0 every update saves, and net/http serves requests in parallel, so two
	// saves can overlap and rename an older snapshot over a newer one.
	saveMu sync.Mutex
}

// New creates the file's parent directory, so a first start with the default path works.
// With restore it also loads the file, so the caller gets a storage that is ready to serve.
func New(mem *storage.MemStorage, path string, interval time.Duration, restore bool, lg *slog.Logger) (*Storage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create dir for %s: %w", path, err)
	}
	s := &Storage{MemStorage: mem, path: path, interval: interval, log: lg}
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

	// Writes go to the embedded MemStorage, not s, so a restore does not save the file it reads.
	for _, m := range list {
		switch m.MType {
		case metrics.KindGauge:
			if m.Value == nil {
				return fmt.Errorf("%s: gauge %q has no value", s.path, m.ID)
			}
			s.MemStorage.SetGauge(m.ID, metrics.Gauge(*m.Value))
		case metrics.KindCounter:
			if m.Delta == nil {
				return fmt.Errorf("%s: counter %q has no delta", s.path, m.ID)
			}
			s.MemStorage.AddCounter(m.ID, metrics.Counter(*m.Delta))
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

	data, err := json.MarshalIndent(toModels(s.Snapshot()), "", "  ")
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

func (s *Storage) SetGauge(name string, v metrics.Gauge) error {
	s.MemStorage.SetGauge(name, v)
	return s.saveIfSync()
}

func (s *Storage) AddCounter(name string, v metrics.Counter) error {
	s.MemStorage.AddCounter(name, v)
	return s.saveIfSync()
}

// saveIfSync returns a failed save without logging it, since the handler logs the error it
// answers 500 with. The value stays in memory either way, so a client retry adds a counter twice.
func (s *Storage) saveIfSync() error {
	if s.interval != 0 {
		return nil
	}
	return s.Save()
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
