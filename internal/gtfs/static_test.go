package gtfs

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/metrics"
	"maglev.onebusaway.org/internal/models"
)

func TestNewGTFSDBConfig_QueryMetricsRecorder(t *testing.T) {
	t.Run("leaves recorder nil when metrics are disabled", func(t *testing.T) {
		dbConfig := newGTFSDBConfig(":memory:", Config{
			Env: appconf.Test,
		})

		assert.Nil(t, dbConfig.QueryMetricsRecorder)
	})

	t.Run("wires recorder when metrics are enabled", func(t *testing.T) {
		m := metrics.New()
		dbConfig := newGTFSDBConfig(":memory:", Config{
			Env:     appconf.Test,
			Metrics: m,
		})

		assert.Same(t, m, dbConfig.QueryMetricsRecorder)
	})
}

// logMessageCounter is a slog.Handler that counts records by message.
type logMessageCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *logMessageCounter) Enabled(context.Context, slog.Level) bool { return true }
func (c *logMessageCounter) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *logMessageCounter) WithGroup(string) slog.Handler            { return c }
func (c *logMessageCounter) Handle(_ context.Context, record slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[record.Message]++
	return nil
}

func (c *logMessageCounter) count(message string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[message]
}

// countLogMessages routes the default logger to a counter for the rest of the
// test.
func countLogMessages(t *testing.T) *logMessageCounter {
	counter := &logMessageCounter{counts: map[string]int{}}
	previous := slog.Default()
	slog.SetDefault(slog.New(counter))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return counter
}

// TestReloadStaticPrecomputesOnlyWhenChanged checks that stop directions are
// precomputed after an import that changed the data, and not after a reload
// that found the same feed.
func TestReloadStaticPrecomputesOnlyWhenChanged(t *testing.T) {
	counter := countLogMessages(t)
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: filepath.Join(t.TempDir(), "gtfs.db"),
		Env:          appconf.Development,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	require.Equal(t, 1, counter.count("precomputing_stop_directions_started"))

	var withDirection int
	require.NoError(t, manager.GtfsDB.DB.QueryRow(`SELECT COUNT(*) FROM stops WHERE direction IS NOT NULL`).Scan(&withDirection))
	assert.Positive(t, withDirection)

	changed, err := manager.ReloadStatic(context.Background())
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, 1, counter.count("precomputing_stop_directions_started"))
}
