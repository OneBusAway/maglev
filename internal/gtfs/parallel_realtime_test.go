package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestParallelRealtimeUpdates(t *testing.T) {
	// Track server calls to verify parallelism
	var mu sync.Mutex
	callTimes := make([]time.Time, 0, 2)

	// Create test servers that simulate real GTFS-RT endpoints
	mux := http.NewServeMux()

	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callTimes = append(callTimes, time.Now())
		mu.Unlock()

		// Simulate some processing time
		time.Sleep(100 * time.Millisecond)

		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
		if err != nil {
			w.Header().Set("Content-Type", "application/x-protobuf")
			_, _ = w.Write([]byte{})
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callTimes = append(callTimes, time.Now())
		mu.Unlock()

		// Simulate some processing time
		time.Sleep(100 * time.Millisecond)

		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		if err != nil {
			w.Header().Set("Content-Type", "application/x-protobuf")
			_, _ = w.Write([]byte{})
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test that updateFeedRealtime fetches trip and vehicle data in parallel
	t.Run("Parallel fetch within single feed", func(t *testing.T) {
		mu.Lock()
		callTimes = callTimes[:0]
		mu.Unlock()

		feedCfg := RTFeedConfig{
			ID:                  "test-feed",
			TripUpdatesURL:      server.URL + "/trip-updates",
			VehiclePositionsURL: server.URL + "/vehicle-positions",
			RefreshInterval:     30,
			Enabled:             true,
		}

		manager := newTestManager()

		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		manager.updateFeedRealtime(ctx, feedCfg)
		elapsed := time.Since(start)

		mu.Lock()
		numCalls := len(callTimes)
		timeDiff := time.Duration(0)
		if len(callTimes) >= 2 {
			if callTimes[1].After(callTimes[0]) {
				timeDiff = callTimes[1].Sub(callTimes[0])
			} else {
				timeDiff = callTimes[0].Sub(callTimes[1])
			}
		}
		mu.Unlock()

		assert.Equal(t, 2, numCalls, "Should make 2 calls (trips and vehicles)")
		assert.Less(t, elapsed, 200*time.Millisecond, "Parallel calls should be significantly faster than 200ms")
		assert.Less(t, timeDiff, 50*time.Millisecond, "Calls should be made nearly simultaneously")
	})
}

func TestParallelRealtimeUpdatesWithErrors(t *testing.T) {
	// Test error handling in parallel updates
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}))
	defer errorServer.Close()

	feedCfg := RTFeedConfig{
		ID:                  "error-feed",
		TripUpdatesURL:      errorServer.URL + "/trip-updates",
		VehiclePositionsURL: errorServer.URL + "/vehicle-positions",
		RefreshInterval:     30,
		Enabled:             true,
	}

	manager := newTestManager()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should not panic or hang when both endpoints fail
	manager.updateFeedRealtime(ctx, feedCfg)

	// Verify that no data was stored (due to errors)
	trips := manager.GetRealTimeTrips()
	vehicles := manager.GetRealTimeVehicles()

	assert.Empty(t, trips, "No trips should be stored when errors occur")
	assert.Empty(t, vehicles, "No vehicles should be stored when errors occur")
}

func TestParallelRealtimeUpdatesWithContextCancellation(t *testing.T) {
	// Test context cancellation during parallel updates
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow response
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write([]byte{})
	}))
	defer slowServer.Close()

	feedCfg := RTFeedConfig{
		ID:                  "slow-feed",
		TripUpdatesURL:      slowServer.URL + "/trip-updates",
		VehiclePositionsURL: slowServer.URL + "/vehicle-positions",
		RefreshInterval:     30,
		Enabled:             true,
	}

	manager := newTestManager()

	// Create a context with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	manager.updateFeedRealtime(ctx, feedCfg)
	elapsed := time.Since(start)

	// Should return quickly due to context cancellation
	assert.Less(t, elapsed, 150*time.Millisecond, "Should return quickly when context is cancelled")
}

func TestRealTimeDataConsistency(t *testing.T) {
	// Test that parallel updates to multiple feeds maintain data consistency
	manager := newTestManager()

	// Run multiple parallel updates to test for race conditions
	var wg sync.WaitGroup
	ctx := context.Background()

	feedCfg := RTFeedConfig{
		ID:                  "race-feed",
		TripUpdatesURL:      "http://invalid.example.com/trips",
		VehiclePositionsURL: "http://invalid.example.com/vehicles",
		RefreshInterval:     30,
		Enabled:             true,
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.updateFeedRealtime(ctx, feedCfg)
		}()
	}

	wg.Wait()

	// Should not panic or cause data corruption; invalid URLs produce no data.
	assert.Empty(t, manager.GetRealTimeTrips(), "no trips should be stored when all URLs are unreachable")
	assert.Empty(t, manager.GetRealTimeVehicles(), "no vehicles should be stored when all URLs are unreachable")
}

// newTestManager creates a minimal Manager for tests that only exercise realtime code
func newTestManager() *Manager {
	return &Manager{
		realTimeMutex:                  sync.RWMutex{},
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
		feedTrips:                      make(map[string][]gtfs.Trip),
		feedVehicles:                   make(map[string][]gtfs.Vehicle),
		feedAlerts:                     make(map[string][]gtfs.Alert),
		feedAgencyFilter:               make(map[string]map[string]bool),
		feedVehicleLastSeen:            make(map[string]map[string]time.Time),
		feedVehicleTimestamp:           make(map[string]uint64),
	}
}
