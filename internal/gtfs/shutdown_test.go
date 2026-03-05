package gtfs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestManagerShutdown(t *testing.T) {
	// Create a config that uses local file to avoid network calls in tests
	testDataPath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "raba.zip"))
	require.NoError(t, err, "Failed to get test data path")

	config := Config{
		GtfsURL:      testDataPath,
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
		Verbose:      false,
	}

	// Initialize manager
	manager, err := InitGTFSManager(context.Background(), config)
	require.NoError(t, err, "Failed to initialize GTFS manager")
	require.NotNil(t, manager, "Manager should not be nil")

	// Verify manager is functional
	agencies := manager.GetAgencies()
	assert.Greater(t, len(agencies), 0, "Should have loaded agencies")

	// Test shutdown
	done := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(done)
	}()

	// Shutdown should complete within a reasonable time
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown took too long")
	}
}

func TestManagerShutdownWithRealtime(t *testing.T) {
	// Create a config with real-time enabled but invalid URLs to avoid network calls
	testDataPath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "raba.zip"))
	require.NoError(t, err, "Failed to get test data path")

	config := Config{
		GtfsURL:      testDataPath,
		GTFSDataPath: ":memory:",
		RTFeeds: []RTFeedConfig{
			{
				ID:                  "test-feed",
				TripUpdatesURL:      "http://invalid.example.com/trips.pb",
				VehiclePositionsURL: "http://invalid.example.com/vehicles.pb",
				RefreshInterval:     30,
				Enabled:             true,
			},
		},
		Env:     appconf.Test,
		Verbose: false,
	}

	// Initialize manager
	manager, err := InitGTFSManager(context.Background(), config)
	require.NoError(t, err, "Failed to initialize GTFS manager")
	require.NotNil(t, manager, "Manager should not be nil")

	// Give the real-time goroutine a moment to start
	time.Sleep(100 * time.Millisecond)

	// Test shutdown
	done := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(done)
	}()

	// Shutdown should complete within a reasonable time even with real-time goroutine
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown took too long")
	}
}

func TestManagerShutdownIdempotent(t *testing.T) {
	// Create a basic config
	testDataPath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "raba.zip"))
	require.NoError(t, err, "Failed to get test data path")

	config := Config{
		GtfsURL:      testDataPath,
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
		Verbose:      false,
	}

	// Initialize manager
	manager, err := InitGTFSManager(context.Background(), config)
	require.NoError(t, err, "Failed to initialize GTFS manager")

	// Call shutdown multiple times - should not panic or hang
	manager.Shutdown()
	manager.Shutdown() // Second call should be safe
}
