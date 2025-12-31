package gtfs

import (
	"context"
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
					if len(agencies) == 0 {
						// Should not happen if initialized correctly
						// But inside a tight loop with RLock, we just check access
					}

					// 2. Simulate DB Query Access (normally protected by RLock in handler)
					if manager.GtfsDB != nil {
						// We can't easily query DB here without setting up queries fully or mocking
						// but checking the pointer is non-nil is a start.
						// The real handlers call methods on manager which call RLock.
						// Here we are inside RLock manually.
					}
					manager.RUnlock()

					// Also call public methods which use RLock internally
					_ = manager.GetAgencies()

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
