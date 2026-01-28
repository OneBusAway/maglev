package gtfs

import (
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

// testManagerWithMutex is a test helper that extends Manager with a static mutex
type testManagerWithMutex struct {
	Manager
	staticMutex sync.RWMutex
}

func TestConcurrentGTFSDataAccess(t *testing.T) {
	// Create a test manager with some sample data
	manager := &Manager{
		gtfsData: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "test-agency", Name: "Test Agency"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Stop 1"},
				{Id: "stop2", Name: "Stop 2"},
			},
			Routes: []gtfs.Route{
				{Id: "route1", ShortName: "R1"},
			},
		},
		realTimeMutex: sync.RWMutex{},
	}

	// Test concurrent reads
	t.Run("Concurrent reads should not cause data races", func(t *testing.T) {
		var wg sync.WaitGroup
		numGoroutines := 100
		results := make([][]gtfs.Agency, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				// Simulate reading data multiple times
				for j := 0; j < 10; j++ {
					agencies := manager.GetAgencies()
					results[index] = agencies
					time.Sleep(time.Microsecond) // Small delay to increase race chance
				}
			}(i)
		}

		wg.Wait()

		// All results should be the same
		for i := 0; i < numGoroutines; i++ {
			assert.Equal(t, 1, len(results[i]), "Should have one agency")
			assert.Equal(t, "test-agency", results[i][0].Id, "Agency ID should match")
		}
	})

	// Test concurrent read/write without protection (this test demonstrates the problem)
	t.Run("Concurrent read/write without protection should be unsafe", func(t *testing.T) {
		// This test demonstrates the race condition that we need to fix
		// We'll run it with the race detector to catch issues

		var wg sync.WaitGroup
		done := make(chan struct{})

		// Start readers
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
						_ = manager.GetAgencies()
						_ = manager.GetStops()
						_ = manager.GetStaticData()
						time.Sleep(time.Microsecond)
					}
				}
			}()
		}

		// Start writer (simulating the unsafe setStaticGTFS)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				select {
				case <-done:
					return
				default:
					// This is the unsafe operation we're testing
					newData := &gtfs.Static{
						Agencies: []gtfs.Agency{
							{Id: "new-agency", Name: "New Agency"},
						},
						Stops: []gtfs.Stop{
							{Id: "new-stop", Name: "New Stop"},
						},
					}
					manager.unsafeSetStaticGTFS(newData)
					time.Sleep(time.Millisecond)
				}
			}
		}()

		// Let it run for a short time
		time.Sleep(50 * time.Millisecond)
		close(done)
		wg.Wait()
	})
}

func TestSafeGTFSDataAccess(t *testing.T) {
	// Test the safe version with mutex protection
	manager := &testManagerWithMutex{
		Manager: Manager{
			gtfsData: &gtfs.Static{
				Agencies: []gtfs.Agency{
					{Id: "test-agency", Name: "Test Agency"},
				},
			},
			realTimeMutex: sync.RWMutex{},
		},
		staticMutex: sync.RWMutex{}, // This will be added to the real Manager
	}

	// Test concurrent read/write with protection
	t.Run("Concurrent read/write with protection should be safe", func(t *testing.T) {
		var wg sync.WaitGroup
		done := make(chan struct{})
		readResults := make([]string, 100)
		readIndex := 0
		var readMutex sync.Mutex

		// Start readers
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
						agencies := manager.safeGetAgencies() // This will be added in implementation
						if len(agencies) > 0 {
							readMutex.Lock()
							if readIndex < len(readResults) {
								readResults[readIndex] = agencies[0].Id
								readIndex++
							}
							readMutex.Unlock()
						}
						time.Sleep(time.Microsecond)
					}
				}
			}()
		}

		// Start writer
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				select {
				case <-done:
					return
				default:
					newData := &gtfs.Static{
						Agencies: []gtfs.Agency{
							{Id: "safe-agency", Name: "Safe Agency"},
						},
					}
					manager.safeSetStaticGTFS(newData) // This will be added in implementation
					time.Sleep(time.Millisecond)
				}
			}
		}()

		// Let it run for a short time
		time.Sleep(50 * time.Millisecond)
		close(done)
		wg.Wait()

		// Verify that all reads were successful (no panics or nil pointer dereferences)
		readMutex.Lock()
		validReads := 0
		for i := 0; i < readIndex; i++ {
			if readResults[i] != "" {
				validReads++
			}
		}
		readMutex.Unlock()

		assert.Greater(t, validReads, 0, "Should have some successful reads")
	})
}

func TestConcurrentVehicleUpdates(t *testing.T) {
	// Test that real-time data updates are already safe (they use realTimeMutex)
	manager := &Manager{
		gtfsData: &gtfs.Static{
			Routes: []gtfs.Route{},
		},
		realTimeVehicles: []gtfs.Vehicle{},
		realTimeMutex:    sync.RWMutex{},
		staticMutex:      sync.RWMutex{},
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = manager.VehiclesForAgencyID("test")
					time.Sleep(time.Microsecond)
				}
			}
		}()
	}

	// Start writers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				select {
				case <-done:
					return
				default:
					// Use direct assignment to realTimeVehicles for testing
					manager.realTimeMutex.Lock()
					testVehicleID := gtfs.VehicleID{ID: "test-vehicle"}
					manager.realTimeVehicles = []gtfs.Vehicle{
						{ID: &testVehicleID},
					}
					manager.realTimeMutex.Unlock()
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Let it run for a short time
	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Should complete without races (tested with race detector)
}

// Helper methods for testing - these simulate the unsafe operations
func (manager *Manager) unsafeSetStaticGTFS(staticData *gtfs.Static) {
	// This is the current unsafe implementation
	manager.gtfsData = staticData
	manager.lastUpdated = time.Now()
}

// Helper methods for testing - these simulate the safe operations that will be implemented
func (tm *testManagerWithMutex) safeSetStaticGTFS(staticData *gtfs.Static) {
	// This will be implemented with proper mutex protection
	tm.staticMutex.Lock()
	defer tm.staticMutex.Unlock()
	tm.gtfsData = staticData
	tm.lastUpdated = time.Now()
}

func (tm *testManagerWithMutex) safeGetAgencies() []gtfs.Agency {
	// This will be implemented with proper mutex protection
	tm.RLock()
	defer tm.RUnlock()
	if tm.gtfsData == nil {
		return []gtfs.Agency{}
	}
	return tm.gtfsData.Agencies
}
