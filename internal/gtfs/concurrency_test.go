package gtfs

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConcurrentFrequencyAccess(t *testing.T) {
	// Test that our O(1) in-memory frequency map is thread-safe
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
