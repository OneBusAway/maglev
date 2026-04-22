package gtfs

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func loggerErrorf(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	return err
}

func TestHotSwap_QueriesCompleteDuringSwap(t *testing.T) {
	ctx := context.Background()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}
	tempDir := t.TempDir()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	agencies, err := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "25", agencies[0].ID)

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readerCount := 5
	wg.Add(readerCount)
	errChan := make(chan error, readerCount)

	for i := 0; i < readerCount; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					aps, err := manager.GtfsDB.Queries.ListAgencies(ctx)
					if err != nil && ctx.Err() == nil {
						errChan <- loggerErrorf("Failed to list agencies during read: %v", err)
					}
					if len(aps) == 0 && ctx.Err() == nil {
						errChan <- loggerErrorf("No agencies found during read")
					}

					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	newSource := models.GetFixturePath(t, "gtfs.zip")
	manager.SetGtfsURL(newSource)

	_, err = manager.ReloadStatic(context.Background())
	assert.Nil(t, err, "reloadstatic should succeed with new file")

	cancel()
	wg.Wait()
	close(errChan)

	for e := range errChan {
		t.Errorf("Reader error: %v", e)
	}

	agencies, err = manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "40", agencies[0].ID)
}

func TestHotSwap_FailureRecovery(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	agencies, err := manager.GtfsDB.Queries.ListAgencies(context.Background())
	if err != nil {
		t.Fatalf("Failed to list agencies: %v", err)
	}
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "25", agencies[0].ID)

	manager.SetGtfsURL("/path/to/non/existent/file.zip")

	_, err = manager.ReloadStatic(context.Background())
	assert.Error(t, err, "ReloadStatic should fail with invalid source")

	agencies, err = manager.GtfsDB.Queries.ListAgencies(context.Background())
	assert.Nil(t, err)
	assert.Equal(t, 1, len(agencies), "Original data should be preserved")
	assert.Equal(t, "25", agencies[0].ID, "Should still be using original agency")
}

func TestHotSwap_OldDatabaseCleanup(t *testing.T) {
	ctx := context.Background()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}
	tempDir := t.TempDir()

	gtfsOriginal := models.GetFixturePath(t, "raba.zip")
	gtfsNew := models.GetFixturePath(t, "gtfs.zip")

	gtfsConfig := Config{
		GtfsURL:      gtfsOriginal,
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	manager.SetGtfsURL(gtfsNew)
	_, err = manager.ReloadStatic(context.Background())
	require.NoError(t, err, "ReloadStatic failed for new GTFS")

	agencies, err := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, agencies, "No agencies found after second update")
	assert.Equal(t, "40", agencies[0].ID)
}

func TestHotSwap_MutexProtectedSwap(t *testing.T) {
	ctx := context.Background()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}
	tempDir := t.TempDir()

	gtfsOriginal := models.GetFixturePath(t, "raba.zip")
	gtfsNew := models.GetFixturePath(t, "gtfs.zip")

	gtfsConfig := Config{
		GtfsURL:      gtfsOriginal,
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	// Verify initial state
	initialAgencies, err := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, initialAgencies)
	assert.Equal(t, "25", initialAgencies[0].ID)
	initialLayoverCount := countBlockLayovers(t, manager)

	// Capture old references
	oldGtfsDB := manager.GtfsDB

	manager.SetGtfsURL(gtfsNew)
	_, err = manager.ReloadStatic(context.Background())
	assert.Nil(t, err, "ReloadStatic should succeed")

	// Verify Final State
	updatedAgencies, listErr := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, listErr)
	require.NotEmpty(t, updatedAgencies)
	assert.Equal(t, "40", updatedAgencies[0].ID)

	// DB is reused in-place: the client pointer is stable across reloads.
	assert.Same(t, oldGtfsDB, manager.GtfsDB, "GtfsDB should be reused in place")
	// block_layover rows are rebuilt from the new feed.
	updatedLayoverCount := countBlockLayovers(t, manager)
	assert.NotEqual(t, initialLayoverCount, updatedLayoverCount, "block_layover rows should be rebuilt from the new feed")
}

// countBlockLayovers returns the number of rows in the block_layover table.
func countBlockLayovers(t *testing.T, manager *Manager) int {
	t.Helper()
	var n int
	err := manager.GtfsDB.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM block_layover").Scan(&n)
	require.NoError(t, err)
	return n
}

func TestHotSwap_ConcurrentForceUpdate(t *testing.T) {
	ctx := context.Background()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}
	tempDir := t.TempDir()

	// Initial setup with "raba.zip"
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown()

	// Verify initial state
	initialAgencies, err := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, initialAgencies)
	assert.Equal(t, "25", initialAgencies[0].ID)

	// Prepare to update to "gtfs.zip"
	newSource := models.GetFixturePath(t, "gtfs.zip")
	manager.SetGtfsURL(newSource)

	// Launch concurrent ForceUpdate calls
	concurrency := 2
	errChan := make(chan error, concurrency)
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			_, err := manager.ReloadStatic(context.Background())
			errChan <- err
		}()
	}

	wg.Wait()
	close(errChan)

	// Both updates should succeed (serialized one after another)
	// OR essentially one might overwrite the other's result, but neither should crash.
	for err := range errChan {
		assert.NoError(t, err, "Concurrent ForceUpdate should not return error")
	}

	// Verify final state matches "gtfs.zip" (agency ID 40)
	finalAgencies, listErr := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, listErr)
	if len(finalAgencies) > 0 {
		assert.Equal(t, "40", finalAgencies[0].ID, "Should utilize new GTFS data")
	} else {
		t.Error("Agencies should not be empty after update")
	}
}
