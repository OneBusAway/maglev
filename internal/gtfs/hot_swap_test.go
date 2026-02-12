package gtfs

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func loggerErrorf(format string, args ...interface{}) error {
	err := fmt.Errorf(format, args...)
	return err
}

func TestHotSwap_QueriesCompleteDuringSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite file I/O is too slow for CI timeout")
	}
	tempDir := t.TempDir()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	agencies := manager.GetAgencies()
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "25", agencies[0].Id)

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
					manager.RLock()
					if manager.gtfsData == nil {
						errChan <- loggerErrorf("gtfsData is nil during read")
					}
					if manager.GtfsDB == nil {
						errChan <- loggerErrorf("GtfsDB is nil during read")
						manager.RUnlock()
						continue
					}

					aps, err := manager.GtfsDB.Queries.ListAgencies(ctx)
					if err != nil && ctx.Err() == nil {
						errChan <- loggerErrorf("Failed to list agencies during read: %v", err)
					}
					if len(aps) == 0 && ctx.Err() == nil {
						errChan <- loggerErrorf("No agencies found during read")
					}

					time.Sleep(5 * time.Millisecond)
					manager.RUnlock()
				}
			}
		}()
	}

	newSource := models.GetFixturePath(t, "gtfs.zip")
	manager.SetGtfsURL(newSource)

	time.Sleep(50 * time.Millisecond)

	err = manager.ForceUpdate(context.Background())
	assert.Nil(t, err, "ForceUpdate should succeed with new file")

	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	close(errChan)

	for e := range errChan {
		t.Errorf("Reader error: %v", e)
	}

	agencies = manager.GetAgencies()
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "40", agencies[0].Id)
}

func TestHotSwap_FailureRecovery(t *testing.T) {

	tempDir := t.TempDir()
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(gtfsConfig)
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

	err = manager.ForceUpdate(context.Background())

	assert.Error(t, err, "ForceUpdate should fail with invalid source")

	// Verify temp file is cleaned up
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f.Name(), "temp.db") {
			t.Errorf("Found temp DB file that should have been cleaned up: %s", f.Name())
		}
	}

	agencies, err = manager.GtfsDB.Queries.ListAgencies(context.Background())
	assert.Nil(t, err)
	assert.Equal(t, 1, len(agencies), "Original data should be preserved")
	assert.Equal(t, "25", agencies[0].ID, "Should still be using original agency")
}

func TestHotSwap_OldDatabaseCleanup(t *testing.T) {
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

	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	manager.SetGtfsURL(gtfsNew)
	err = manager.ForceUpdate(context.Background())
	require.NoError(t, err, "ForceUpdate failed for new GTFS")

	agencies := manager.GetAgencies()
	require.NotEmpty(t, agencies, "No agencies found after second update")
	assert.Equal(t, "40", agencies[0].Id)

	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f.Name(), "temp.db") {
			t.Errorf("Found temp DB file that should have been cleaned up: %s", f.Name())
		}
	}

}

func TestHotSwap_MutexProtectedSwap(t *testing.T) {
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

	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	// Verify initial state
	manager.RLock()
	assert.Equal(t, "25", manager.gtfsData.Agencies[0].Id)
	assert.NotNil(t, manager.stopSpatialIndex)
	assert.NotNil(t, manager.blockLayoverIndices)
	manager.RUnlock()

	// Capture old references
	manager.RLock()
	oldStaticData := manager.gtfsData
	oldGtfsDB := manager.GtfsDB
	oldSpatialIndex := manager.stopSpatialIndex
	oldBlockLayoverIndices := manager.blockLayoverIndices
	manager.RUnlock()

	manager.SetGtfsURL(gtfsNew)
	err = manager.ForceUpdate(context.Background())
	assert.Nil(t, err, "ForceUpdate should succeed")

	// 4. Verify Final State
	manager.RLock()
	assert.Equal(t, "40", manager.gtfsData.Agencies[0].Id)

	// Verify memory cleanup (references replaced)
	assert.NotEqual(t, oldStaticData, manager.gtfsData, "StaticData Reference should have been replaced")
	assert.NotEqual(t, oldGtfsDB, manager.GtfsDB, "GtfsDB Reference should have been replaced")
	assert.NotEqual(t, oldSpatialIndex, manager.stopSpatialIndex, "SpatialIndex Reference should have been replaced")
	assert.NotEqual(t, oldBlockLayoverIndices, manager.blockLayoverIndices, "BlockLayoverIndices Reference should have been replaced")

	assert.NotNil(t, manager.stopSpatialIndex)
	assert.NotNil(t, manager.blockLayoverIndices)

	manager.RUnlock()

}

func TestHotSwap_ConcurrentForceUpdate(t *testing.T) {
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

	manager, err := InitGTFSManager(gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown()

	// Verify initial state
	manager.RLock()
	assert.Equal(t, "25", manager.gtfsData.Agencies[0].Id)
	manager.RUnlock()

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
			// Calling ForceUpdate concurrently
			err := manager.ForceUpdate(context.Background())
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
	manager.RLock()
	defer manager.RUnlock()
	if len(manager.gtfsData.Agencies) > 0 {
		assert.Equal(t, "40", manager.gtfsData.Agencies[0].Id, "Should utilize new GTFS data")
	} else {
		t.Error("Agencies should not be empty after update")
	}
}
