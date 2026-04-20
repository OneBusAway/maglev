package gtfs

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestManager_HealthState(t *testing.T) {
	mgr := &Manager{
		isHealthy:                      false,
		staticMutex:                    sync.RWMutex{},
		realTimeMutex:                  sync.RWMutex{},
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
	}

	// Verify initial state is unhealthy
	assert.False(t, mgr.IsHealthy(), "Manager should be unhealthy initially")

	// Test MarkHealthy
	mgr.MarkHealthy()
	assert.True(t, mgr.IsHealthy(), "Manager should be healthy after MarkHealthy()")

	// Test multiple transitions
	mgr.MarkHealthy()
	assert.True(t, mgr.IsHealthy(), "Manager should be healthy")
	mgr.MarkHealthy()
	assert.True(t, mgr.IsHealthy(), "Manager should still be healthy after second MarkHealthy")
}

func TestHealthCheck_Concurrency(t *testing.T) {
	mgr := &Manager{
		isHealthy:                      true,
		staticMutex:                    sync.RWMutex{},
		realTimeMutex:                  sync.RWMutex{},
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
	}

	doneUpdate := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			select {
			case <-doneUpdate:
				return
			default:
				_ = mgr.IsHealthy()
				time.Sleep(1 * time.Millisecond)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			mgr.MarkHealthy()
			time.Sleep(2 * time.Millisecond)
		}
		close(doneUpdate)
	}()

	// Wait for all goroutines to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Concurrency test passed without deadlock")
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out - likely deadlock in mutex handling")
	}
}
