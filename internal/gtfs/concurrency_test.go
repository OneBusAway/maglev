package gtfs

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

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
		staticMutex:   sync.RWMutex{}, // Ensure staticMutex is initialized
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
					manager.RLock() // Acquire proper read lock
					agencies := manager.GetAgencies()
					manager.RUnlock()

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
		if os.Getenv("RUN_RACE_DEMO") == "" {
			t.Skip("Intentional race condition demo; set RUN_RACE_DEMO=1 to run")
		}
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
	manager := &Manager{
		gtfsData: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "test-agency", Name: "Test Agency"},
			},
		},
		realTimeMutex: sync.RWMutex{},
		staticMutex:   sync.RWMutex{},
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
						// thread-safe read via staticMutex
						agencies := manager.safeGetAgencies()
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
					// thread-safe write via staticMutex
					manager.safeSetStaticGTFS(newData)
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

// Helper methods for testing - simplified mutex-protected accessors
func (manager *Manager) safeSetStaticGTFS(staticData *gtfs.Static) {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()
	manager.gtfsData = staticData
	manager.lastUpdated = time.Now()
}

func (manager *Manager) safeGetAgencies() []gtfs.Agency {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	if manager.gtfsData == nil {
		return []gtfs.Agency{}
	}
	return manager.gtfsData.Agencies
}

func TestConcurrentFrequencyAccess(t *testing.T) {
	// Test that our new O(1) in-memory frequency map is thread-safe
	// when being read by API handlers while the static data is reloading

	manager := &Manager{
		frequencyTripIDs: make(map[string]struct{}),
	}
	manager.frequencyTripIDs["freq_trip_1"] = struct{}{}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start Readers (simulating API handlers calling IsFrequencyBasedTrip)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					// Read from the cache. This uses manager.staticMutex.RLock() internally
					_ = manager.IsFrequencyBasedTrip("freq_trip_1")
					_ = manager.IsFrequencyBasedTrip("normal_trip")
					time.Sleep(time.Microsecond)
				}
			}
		}()
	}

	// Start Writer (simulating static data reload updating the cache)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			select {
			case <-done:
				return
			default:
				// Simulate the hot-swap of the frequency cache
				newFreqMap := make(map[string]struct{})
				newFreqMap["freq_trip_2"] = struct{}{}
				newFreqMap["freq_trip_3"] = struct{}{}

				manager.staticMutex.Lock()
				manager.frequencyTripIDs = newFreqMap
				manager.staticMutex.Unlock()

				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// Let the race happen for a short time
	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Verify the cache was actually updated by the writer
	isFreq := manager.IsFrequencyBasedTrip("freq_trip_2")
	assert.True(t, isFreq, "Expected freq_trip_2 to be in the frequency cache after writer finishes")
}
