package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
)

// Benchmark for map rebuild optimization
func BenchmarkRebuildRealTimeTripLookup(b *testing.B) {
	// Create a manager with sample data
	manager := &Manager{
		realTimeTrips: make([]gtfs.Trip, 1000),
	}

	// Populate with sample trips
	for i := 0; i < 1000; i++ {
		manager.realTimeTrips[i] = gtfs.Trip{
			ID: gtfs.TripID{ID: string(rune(i))},
		}
	}

	// Initialize the map
	manager.realTimeTripLookup = make(map[string]int)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rebuildRealTimeTripLookup(manager)
	}
}

func BenchmarkRebuildRealTimeVehicleLookupByTrip(b *testing.B) {
	// Create a manager with sample data
	manager := &Manager{
		realTimeVehicles: make([]gtfs.Vehicle, 1000),
	}

	// Populate with sample vehicles
	for i := 0; i < 1000; i++ {
		tripID := string(rune(i))
		manager.realTimeVehicles[i] = gtfs.Vehicle{
			Trip: &gtfs.Trip{
				ID: gtfs.TripID{ID: tripID},
			},
		}
	}

	// Initialize the map
	manager.realTimeVehicleLookupByTrip = make(map[string]int)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rebuildRealTimeVehicleLookupByTrip(manager)
	}
}

func BenchmarkRebuildRealTimeVehicleLookupByVehicle(b *testing.B) {
	// Create a manager with sample data
	manager := &Manager{
		realTimeVehicles: make([]gtfs.Vehicle, 1000),
	}

	// Populate with sample vehicles
	for i := 0; i < 1000; i++ {
		vehicleID := string(rune(i))
		manager.realTimeVehicles[i] = gtfs.Vehicle{
			ID: &gtfs.VehicleID{ID: vehicleID},
		}
	}

	// Initialize the map
	manager.realTimeVehicleLookupByVehicle = make(map[string]int)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rebuildRealTimeVehicleLookupByVehicle(manager)
	}
}
