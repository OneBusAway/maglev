package gtfs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func TestManager_HotSwapConcurrency(t *testing.T) {
	// 1. Setup Manager with initial data
	// Create a temp dir for this test
	tempDir := t.TempDir()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development, // Use Development to allow file-based DB creation (Test env might force :memory:)
	}

	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	// 2. Start Readers
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readerCount := 10
	wg.Add(readerCount)

	for i := 0; i < readerCount; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Simulate read access
					manager.RLock()
					// 1. Access Static Data
					agencies := manager.gtfsData.Agencies
					if len(agencies) == 0 { //nolint
						// Should not happen if initialized correctly
						// But inside a tight loop with RLock, we just check access
					}

					// 2. Simulate DB Query Access (normally protected by RLock in handler)
					if manager.GtfsDB != nil { //nolint
						// We can't easily query DB here without setting up queries fully or mocking
						// but checking the pointer is non-nil is a start.
						// The real handlers call methods on manager which call RLock.
						// Here we are inside RLock manually.
					}
					
					_ = manager.GetAgencies()

					manager.RUnlock()

					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
	}

	// 3. Perform Hot Swap
	// We will call ForceUpdate.

	// Let readers run for a bit
	time.Sleep(100 * time.Millisecond)

	err = manager.ForceUpdate(context.Background())
	assert.Nil(t, err, "ForceUpdate should succeed")

	// Let readers run a bit more after update
	time.Sleep(100 * time.Millisecond)

	// Stop readers
	cancel()
	wg.Wait()

	// 4. Verify Post-Swap State
	agencies := manager.GetAgencies()
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "25", agencies[0].Id)
}

func TestForceUpdate_FileCleanup(t *testing.T) {
	// 1. Setup Manager
	tempDir := t.TempDir()
	dbPath := tempDir + "/gtfs.db"

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: dbPath,
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	// Verify initial DB exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("Initial DB missing at %s", dbPath)
	}

	// 2. Perform ForceUpdate
	err = manager.ForceUpdate(context.Background())
	assert.Nil(t, err, "ForceUpdate should succeed")

	// 3. Verify Filesystem State
	// gtfs.db should exist
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Final DB missing at %s", dbPath)
	}

	tempPath := strings.TrimSuffix(dbPath, ".db") + ".temp.db"
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("Temp DB should have been cleaned up/renamed: %s", tempPath)
	}

	// Verify Database is actually usable
	agencies := manager.GetAgencies()
	assert.Equal(t, 1, len(agencies), "Should still have data accessible")
}

func TestHotSwap_QueriesCompleteDuringSwap(t *testing.T) {
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
					// Perform a read. We expect this to always succeed.
					// Mixed usage of direct access (simulating query execution) and helper methods
					manager.RLock()
					if manager.gtfsData == nil {
						errChan <- loggerErrorf("gtfsData is nil during read")
					}
					if manager.GtfsDB == nil {
						errChan <- loggerErrorf("GtfsDB is nil during read")
					}
					

					// Use a safe accessor
					aps := manager.GetAgencies()
					if len(aps) == 0 {
						errChan <- loggerErrorf("No agencies found during read")
					}

					time.Sleep(5 * time.Millisecond)
					manager.RUnlock()
				}
			}
		}()
	}


	newSource := models.GetFixturePath(t, "gtfs.zip")
	manager.gtfsSource = newSource

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

	agencies := manager.GetAgencies()
	assert.Equal(t, 1, len(agencies))
	assert.Equal(t, "25", agencies[0].Id)

	manager.gtfsSource = "/path/to/non/existent/file.zip"

	err = manager.ForceUpdate(context.Background())
	
	assert.Error(t, err, "ForceUpdate should fail with invalid source")

	agencies = manager.GetAgencies()
	assert.Equal(t, 1, len(agencies), "Original data should be preserved")
	assert.Equal(t, "25", agencies[0].Id, "Should still be using original agency")
}



func loggerErrorf(format string, args ...interface{}) error {
	err := fmt.Errorf(format, args...)
	return err
}

func TestHotSwap_OldDatabaseCleanup(t *testing.T) {
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
	
	agencies := manager.GetAgencies()
	assert.Equal(t, "25", agencies[0].Id)
	
	readerStarted := make(chan struct{})
	readerFinished := make(chan struct{})
	
	go func() {
		defer close(readerFinished)
		
		manager.RLock()
		defer manager.RUnlock()
		
		close(readerStarted)
		
		// Simulate long read - wait for signal to release
		time.Sleep(100 * time.Millisecond)
		
		agenciesWithLock := manager.GetAgencies()
		if len(agenciesWithLock) == 0 || agenciesWithLock[0].Id != "25" {
			t.Errorf("Reader should see old data during lock, got: %+v", agenciesWithLock)
		}
		
		if manager.GtfsDB == nil {
			t.Error("GtfsDB is nil inside lock")
		}
	}()
	
	<-readerStarted
	
	manager.gtfsSource = gtfsNew
	
	updateDone := make(chan error)
	go func() {
		updateDone <- manager.ForceUpdate(context.Background())
	}()
	
	select {
	case <-updateDone:
		t.Fatal("ForceUpdate should have blocked while RLock is held")
	case <-time.After(50 * time.Millisecond):
	}
	
	<-readerFinished
	
	select {
	case err := <-updateDone:
		assert.Nil(t, err, "ForceUpdate should succeed after lock release")
	case <-time.After(1 * time.Second):
		t.Fatal("ForceUpdate timed out after lock release")
	}
	
	agencies = manager.GetAgencies()
	assert.Equal(t, "40", agencies[0].Id)
	
	for i := 0; i < 3; i++ {
		// Swap back to Original
		manager.gtfsSource = gtfsOriginal
		err = manager.ForceUpdate(context.Background())
		assert.Nil(t, err)
		assert.Equal(t, "25", manager.GetAgencies()[0].Id)
		
		// Swap to New
		manager.gtfsSource = gtfsNew
		err = manager.ForceUpdate(context.Background())
		assert.Nil(t, err)
		assert.Equal(t, "40", manager.GetAgencies()[0].Id)
	}
	
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f.Name(), ".temp.db") {
			t.Errorf("Found temp DB file that should have been cleaned up: %s", f.Name())
		}
	}
}
