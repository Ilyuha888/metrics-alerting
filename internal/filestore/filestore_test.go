package filestore_test

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/filestore"
	"github.com/Ilyuha888/metrics-alerting/internal/metrics"
	"github.com/Ilyuha888/metrics-alerting/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newStorage(t *testing.T, path string, interval time.Duration) *filestore.Storage {
	t.Helper()
	st, err := filestore.New(storage.NewMemStorage(), path, interval, discard)
	require.NoError(t, err)
	return st
}

func TestStorage_Load_RestoresSavedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	saved := newStorage(t, path, time.Hour)
	saved.SetGauge("temp", 36.6)
	saved.SetGauge("big", 1257894000000000000)
	saved.AddCounter("hits", 527)
	require.NoError(t, saved.Save())

	restored := newStorage(t, path, time.Hour)
	require.NoError(t, restored.Load())

	assert.Equal(t, saved.Snapshot(), restored.Snapshot())
}

func TestStorage_Load_MissingFileLeavesStorageEmpty(t *testing.T) {
	st := newStorage(t, filepath.Join(t.TempDir(), "metrics.json"), time.Hour)

	require.NoError(t, st.Load())

	assert.Equal(t, metrics.Snapshot{Gauges: map[string]metrics.Gauge{}, Counters: map[string]metrics.Counter{}}, st.Snapshot())
}

func TestStorage_Load_BadFileIsAnError(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "invalid JSON", content: `[{"id":`, wantErr: "parse "},
		{name: "not an array", content: `{"id":"temp","type":"gauge","value":1}`, wantErr: "parse "},
		{name: "gauge without value", content: `[{"id":"temp","type":"gauge","delta":1}]`, wantErr: `gauge "temp" has no value`},
		{name: "counter without delta", content: `[{"id":"hits","type":"counter","value":1}]`, wantErr: `counter "hits" has no delta`},
		{name: "unknown type", content: `[{"id":"x","type":"histogram","value":1}]`, wantErr: `unknown type "histogram"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "metrics.json")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))
			st := newStorage(t, path, time.Hour)

			err := st.Load()

			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestStorage_Load_DoesNotRewriteTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	content := []byte(`[{"id":"temp","type":"gauge","value":36.6},{"id":"hits","type":"counter","delta":5}]`)
	require.NoError(t, os.WriteFile(path, content, 0o644))
	st := newStorage(t, path, 0)

	require.NoError(t, st.Load())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestStorage_Save_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	st := newStorage(t, filepath.Join(dir, "metrics.json"), time.Hour)
	st.SetGauge("temp", 36.6)

	require.NoError(t, st.Save())
	require.NoError(t, st.Save())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "metrics.json", entries[0].Name())
}

func TestStorage_Save_ReplacesOldContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	require.NoError(t, os.WriteFile(path, []byte(`[{"id":"old","type":"gauge","value":1}]`), 0o644))
	st := newStorage(t, path, time.Hour)
	st.AddCounter("hits", 5)

	require.NoError(t, st.Save())

	restored := newStorage(t, path, time.Hour)
	require.NoError(t, restored.Load())
	assert.Equal(t, metrics.Snapshot{
		Gauges:   map[string]metrics.Gauge{},
		Counters: map[string]metrics.Counter{"hits": 5},
	}, restored.Snapshot())
}

func TestNew_CreatesMissingParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dumps", "nested")

	newStorage(t, filepath.Join(dir, "metrics.json"), time.Hour)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestStorage_SetGauge_SavesAtOnceOnlyWithZeroInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		wantFile bool
	}{
		{name: "zero interval writes the file", interval: 0, wantFile: true},
		{name: "positive interval leaves it to the ticker", interval: time.Hour, wantFile: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "metrics.json")
			st := newStorage(t, path, tt.interval)

			st.SetGauge("temp", 36.6)

			_, err := os.Stat(path)
			assert.Equal(t, tt.wantFile, err == nil)
		})
	}
}

func TestStorage_AddCounter_ZeroIntervalWritesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	st := newStorage(t, path, 0)

	st.AddCounter("hits", 5)

	restored := newStorage(t, path, time.Hour)
	require.NoError(t, restored.Load())
	got, err := restored.Counter("hits")
	require.NoError(t, err)
	assert.Equal(t, metrics.Counter(5), got)
}

func TestStorage_SetGauge_FailedSaveIsLoggedAndValueKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")
	var logs bytes.Buffer
	st, err := filestore.New(storage.NewMemStorage(), path, 0, slog.New(slog.NewTextHandler(&logs, nil)))
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(path, 0o755))

	st.SetGauge("temp", 36.6)

	assert.Contains(t, logs.String(), "save metrics")
	got, err := st.Gauge("temp")
	require.NoError(t, err)
	assert.Equal(t, metrics.Gauge(36.6), got)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "metrics.json", entries[0].Name())
}

func TestStorage_SaveEvery_WritesTheFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dumps")
	path := filepath.Join(dir, "metrics.json")
	st := newStorage(t, path, 10*time.Millisecond)
	st.SetGauge("temp", 36.6)
	// SaveEvery cannot be stopped, so it may write while TempDir cleans up. Once dir is
	// gone every later save fails, and the retry outlasts a save that was mid-write.
	t.Cleanup(func() {
		assert.Eventually(t, func() bool { return os.RemoveAll(dir) == nil }, time.Second, 10*time.Millisecond)
	})

	go st.SaveEvery()

	assert.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, time.Second, 5*time.Millisecond)
}
