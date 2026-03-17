package gtfs

import (
	"context"
	"errors"
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

	// Test shutdown with generous context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = manager.Shutdown(ctx)
	assert.NoError(t, err, "Shutdown should complete without error")
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

	// Test shutdown with generous context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = manager.Shutdown(ctx)
	assert.NoError(t, err, "Shutdown should complete without error even with real-time goroutine")
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
	err = manager.Shutdown(context.Background())
	assert.NoError(t, err)

	err = manager.Shutdown(context.Background()) // Second call should be safe
	assert.NoError(t, err)
}

// TestManagerShutdown_CleanPath verifies that Shutdown returns nil when all
// goroutines exit promptly before the context deadline.
func TestManagerShutdown_CleanPath(t *testing.T) {
	manager := &Manager{
		shutdownChan: make(chan struct{}),
	}

	manager.wg.Add(1)
	go func() {
		defer manager.wg.Done()
		<-manager.shutdownChan
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Shutdown(ctx)
	assert.NoError(t, err, "Shutdown should succeed when goroutines exit promptly")
}

// TestManagerShutdown_TimeoutPath verifies that Shutdown returns an error wrapping
// context.DeadlineExceeded when a goroutine is stuck and ignores shutdownChan.
func TestManagerShutdown_TimeoutPath(t *testing.T) {
	manager := &Manager{
		shutdownChan: make(chan struct{}),
	}

	manager.wg.Add(1)
	stuck := make(chan struct{})
	go func() {
		defer manager.wg.Done()
		<-stuck
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := manager.Shutdown(ctx)
	require.Error(t, err, "Shutdown should return an error when context expires")
	assert.True(t, errors.Is(err, context.DeadlineExceeded),
		"error should wrap context.DeadlineExceeded, got: %v", err)
	assert.Contains(t, err.Error(), "shutdown timeout exceeded")

	close(stuck)
	
}
