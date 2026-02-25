package gtfs

import (
	"fmt"
	"sync"
	"testing"

	"github.com/OneBusAway/go-gtfs"
)

// Benchmark for map rebuild optimization
func BenchmarkRebuildRealTimeTripLookup(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData:      make(map[string]*FeedData),
	}

	feedTrips := make([]gtfs.Trip, 1000)
	for i := 0; i < 1000; i++ {
		feedTrips[i] = gtfs.Trip{
			ID: gtfs.TripID{ID: fmt.Sprintf("trip_%d", i)},
		}
	}
	manager.feedData["feed-0"] = &FeedData{Trips: feedTrips}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.buildMergedRealtime()
	}
}

func BenchmarkRebuildRealTimeVehicleLookupByTrip(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData:      make(map[string]*FeedData),
	}

	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		feedVehicles[i] = gtfs.Vehicle{
			Trip: &gtfs.Trip{
				ID: gtfs.TripID{ID: fmt.Sprintf("trip_%d", i)},
			},
		}
	}
	manager.feedData["feed-0"] = &FeedData{Vehicles: feedVehicles}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.buildMergedRealtime()
	}
}

func BenchmarkRebuildRealTimeVehicleLookupByVehicle(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData:      make(map[string]*FeedData),
	}

	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		feedVehicles[i] = gtfs.Vehicle{
			ID: &gtfs.VehicleID{ID: fmt.Sprintf("vehicle_%d", i)},
		}
	}
	manager.feedData["feed-0"] = &FeedData{Vehicles: feedVehicles}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.buildMergedRealtime()
	}
}
