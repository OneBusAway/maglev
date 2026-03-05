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
		staticMutex:   sync.RWMutex{},
		realTimeMutex: sync.RWMutex{},
	}

	// Test concurrent reads — all readers hold RLock() as the API requires.
	t.Run("Concurrent reads should not cause data races", func(t *testing.T) {
		var wg sync.WaitGroup
		numGoroutines := 100
		results := make([][]gtfs.Agency, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					manager.RLock()
					agencies := manager.GetAgencies()
					results[index] = agencies
					manager.RUnlock()
					time.Sleep(time.Microsecond)
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

	// Test that concurrent reads and writes are safe when the mutex is used correctly.
	// This replaces the previous "unsafe" subtest which intentionally triggered a data
	// race (and therefore always failed under `go test -race`). The unsafe pattern is
	// demonstrated conceptually in the test name; the actual code uses setStaticGTFS
	// which acquires staticMutex, making it safe.
	t.Run("Concurrent read/write with mutex protection is safe", func(t *testing.T) {
		var wg sync.WaitGroup
		done := make(chan struct{})

		// Start readers — each reader correctly acquires RLock before accessing data.
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-done:
						return
					default:
						manager.RLock()
						_ = manager.GetAgencies()
						_ = manager.GetStops()
						_ = manager.GetStaticData()
						manager.RUnlock()
						time.Sleep(time.Microsecond)
					}
				}
			}()
		}

		// Start writer — uses setStaticGTFS which acquires staticMutex internally.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				select {
				case <-done:
					return
				default:
					newData := &gtfs.Static{
						Agencies: []gtfs.Agency{
							{Id: "new-agency", Name: "New Agency"},
						},
						Stops: []gtfs.Stop{
							{Id: "new-stop", Name: "New Stop"},
						},
					}
					// setStaticGTFS acquires staticMutex internally — this is safe.
					manager.setStaticGTFS(newData)
					time.Sleep(time.Millisecond)
				}
			}
		}()

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
			staticMutex:   sync.RWMutex{},
			realTimeMutex: sync.RWMutex{},
		},
		staticMutex: sync.RWMutex{},
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
					manager.safeSetStaticGTFS(newData)
					time.Sleep(time.Millisecond)
				}
			}
		}()

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

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()

	// Should complete without races (tested with race detector)
}

// Helper methods for testManagerWithMutex — simulate the safe operations.
func (tm *testManagerWithMutex) safeSetStaticGTFS(staticData *gtfs.Static) {
	tm.staticMutex.Lock()
	defer tm.staticMutex.Unlock()
	tm.gtfsData = staticData
	tm.lastUpdated = time.Now()
}

func (tm *testManagerWithMutex) safeGetAgencies() []gtfs.Agency {
	tm.staticMutex.RLock()
	defer tm.staticMutex.RUnlock()
	if tm.gtfsData == nil {
		return []gtfs.Agency{}
	}
	return tm.gtfsData.Agencies
}
