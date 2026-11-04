package gtfs

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/metrics"
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

func TestLogFeedExpiryUsesClock(t *testing.T) {
	client, err := gtfsdb.NewClient(gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	manager := &Manager{GtfsDB: client}

	manager.MarkReady()

	expiresAt := time.Date(2025, time.June, 15, 0, 0, 0, 0, time.UTC)
	manager.SetFeedExpiresAtForTest(t.Context(), expiresAt)
	tests := []struct {
		name        string
		currentTime *clock.MockClock
		message     string
	}{
		{
			name:        "Feed is past expiration",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 20, 0, 0, 0, 0, time.UTC)),
			message:     "GTFS feed has expired",
		},
		{
			name:        "Feed expires in 1 day or less",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 14, 12, 0, 0, 0, time.UTC)),
			message:     "GTFS feed expires in 1 day or less",
		},
		{
			name:        "Feed expires in 3 days or less",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 13, 0, 0, 0, 0, time.UTC)),
			message:     "GTFS feed expires in 3 days or less",
		},
		{
			name:        "Feed expires in 7 days or less",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 10, 0, 0, 0, 0, time.UTC)),
			message:     "GTFS feed expires in 7 days or less",
		},
		{
			name:        "Feed is valid well before expiration",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 5, 0, 0, 0, 0, time.UTC)), // daysUntil = 10 (default)
			message:     "GTFS feed valid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
			}))
			manager.config.Clock = test.currentTime

			manager.logFeedExpiry(t.Context(), testLogger)
			assert.Contains(t, buf.String(), test.message, "expected \"%s\" but got \"%s\"", test.message)
		})
	}
}
