package gtfs

import (
	"fmt"
	"sync"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
)

// Benchmark for map rebuild optimization
func BenchmarkRebuildRealTimeTripLookup(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedTrips:     make(map[string][]gtfs.Trip),
	}

	feedTrips := make([]gtfs.Trip, 1000)
	for i := 0; i < 1000; i++ {
		feedTrips[i] = gtfs.Trip{
			ID: gtfs.TripID{ID: fmt.Sprintf("trip_%d", i)},
		}
	}
	manager.feedTrips["feed-0"] = feedTrips

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.rebuildMergedRealtimeLocked()
	}
}

func BenchmarkRebuildRealTimeVehicleLookupByTrip(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedVehicles:  make(map[string][]gtfs.Vehicle),
	}

	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		feedVehicles[i] = gtfs.Vehicle{
			Trip: &gtfs.Trip{
				ID: gtfs.TripID{ID: fmt.Sprintf("trip_%d", i)},
			},
		}
	}
	manager.feedVehicles["feed-0"] = feedVehicles

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.rebuildMergedRealtimeLocked()
	}
}

func BenchmarkRebuildRealTimeVehicleLookupByVehicle(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedVehicles:  make(map[string][]gtfs.Vehicle),
	}

	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		feedVehicles[i] = gtfs.Vehicle{
			ID: &gtfs.VehicleID{ID: fmt.Sprintf("vehicle_%d", i)},
		}
	}
	manager.feedVehicles["feed-0"] = feedVehicles

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.rebuildMergedRealtimeLocked()
	}
}

func BenchmarkRebuildAlertIndex(b *testing.B) {
	routeID := "route_0"
	agencyID := "agency_0"
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedAlerts:    make(map[string][]gtfs.Alert),
	}

	feedAlerts := make([]gtfs.Alert, 1000)
	for i := 0; i < 1000; i++ {
		tripID := gtfs.TripID{ID: fmt.Sprintf("trip_%d", i)}
		stopID := fmt.Sprintf("stop_%d", i)
		feedAlerts[i] = gtfs.Alert{
			ID: fmt.Sprintf("alert_%d", i),
			InformedEntities: []gtfs.AlertInformedEntity{
				{TripID: &tripID},
				{RouteID: &routeID},
				{AgencyID: &agencyID},
				{StopID: &stopID},
			},
		}
	}
	manager.feedAlerts["feed-0"] = feedAlerts

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.rebuildMergedRealtimeLocked()
	}
}

func BenchmarkGetAlertsByIDs(b *testing.B) {
	// Realistic distribution: 1000 alerts spread across 100 routes and 50 agencies
	// (~10 alerts/route, ~20 alerts/agency), each with a unique trip.
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedAlerts:    make(map[string][]gtfs.Alert),
	}

	feedAlerts := make([]gtfs.Alert, 1000)
	for i := 0; i < 1000; i++ {
		tripID := gtfs.TripID{ID: fmt.Sprintf("trip_%d", i)}
		routeID := fmt.Sprintf("route_%d", i%100)
		agencyID := fmt.Sprintf("agency_%d", i%50)
		stopID := fmt.Sprintf("stop_%d", i)
		feedAlerts[i] = gtfs.Alert{
			ID: fmt.Sprintf("alert_%d", i),
			InformedEntities: []gtfs.AlertInformedEntity{
				{TripID: &tripID},
				{RouteID: &routeID},
				{AgencyID: &agencyID},
				{StopID: &stopID},
			},
		}
	}
	manager.feedAlerts["feed-0"] = feedAlerts
	manager.rebuildMergedRealtimeLocked()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := i % 1000
		_ = manager.GetAlertsByIDs(
			fmt.Sprintf("trip_%d", n),
			fmt.Sprintf("route_%d", n%100),
			fmt.Sprintf("agency_%d", n%50),
		)
	}
}

func BenchmarkGetAlertsForStop(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedAlerts:    make(map[string][]gtfs.Alert),
	}

	feedAlerts := make([]gtfs.Alert, 1000)
	for i := 0; i < 1000; i++ {
		stopID := fmt.Sprintf("stop_%d", i)
		feedAlerts[i] = gtfs.Alert{
			ID: fmt.Sprintf("alert_%d", i),
			InformedEntities: []gtfs.AlertInformedEntity{
				{StopID: &stopID},
			},
		}
	}
	manager.feedAlerts["feed-0"] = feedAlerts
	manager.rebuildMergedRealtimeLocked()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetAlertsForStop(fmt.Sprintf("stop_%d", i%1000))
	}
}

func BenchmarkRebuildVehiclesByRoute(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedTrips:     make(map[string][]gtfs.Trip),
		feedVehicles:  make(map[string][]gtfs.Vehicle),
	}

	feedTrips := make([]gtfs.Trip, 1000)
	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		tripID := fmt.Sprintf("trip_%d", i)
		routeID := fmt.Sprintf("route_%d", i%100)
		feedTrips[i] = gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		}
		feedVehicles[i] = gtfs.Vehicle{
			ID: &gtfs.VehicleID{ID: fmt.Sprintf("vehicle_%d", i)},
			Trip: &gtfs.Trip{
				ID: gtfs.TripID{
					ID:                   tripID,
					RouteID:              routeID,
					ScheduleRelationship: gtfsrt.TripDescriptor_DUPLICATED,
				},
			},
		}
	}
	manager.feedTrips["feed-0"] = feedTrips
	manager.feedVehicles["feed-0"] = feedVehicles

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.rebuildMergedRealtimeLocked()
	}
}

func BenchmarkGetDuplicatedVehiclesForRoute(b *testing.B) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedTrips:     make(map[string][]gtfs.Trip),
		feedVehicles:  make(map[string][]gtfs.Vehicle),
	}

	feedTrips := make([]gtfs.Trip, 1000)
	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		tripID := fmt.Sprintf("trip_%d", i)
		routeID := fmt.Sprintf("route_%d", i%100)
		feedTrips[i] = gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		}

		feedVehicles[i] = gtfs.Vehicle{
			ID: &gtfs.VehicleID{ID: fmt.Sprintf("vehicle_%d", i)},
			Trip: &gtfs.Trip{
				ID: gtfs.TripID{
					ID:                   tripID,
					RouteID:              routeID,
					ScheduleRelationship: gtfsrt.TripDescriptor_DUPLICATED,
				},
			},
		}
	}
	manager.feedTrips["feed-0"] = feedTrips
	manager.feedVehicles["feed-0"] = feedVehicles
	manager.rebuildMergedRealtimeLocked()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.GetDuplicatedVehiclesForRoute(fmt.Sprintf("route_%d", i%100))
	}
}

func BenchmarkVehiclesForAgencyID(b *testing.B) {
	routes := make([]*gtfs.Route, 0, 10)
	for i := 0; i < 10; i++ {
		routes = append(routes, &gtfs.Route{Id: fmt.Sprintf("route_%d", i)})
	}

	manager := &Manager{
		staticMutex:   sync.RWMutex{},
		realTimeMutex: sync.RWMutex{},
		routesByAgencyID: map[string][]*gtfs.Route{
			"agency_0": routes,
		},
		feedVehicles: make(map[string][]gtfs.Vehicle),
	}

	feedVehicles := make([]gtfs.Vehicle, 1000)
	for i := 0; i < 1000; i++ {
		feedVehicles[i] = gtfs.Vehicle{
			ID: &gtfs.VehicleID{ID: fmt.Sprintf("vehicle_%d", i)},
			Trip: &gtfs.Trip{
				ID: gtfs.TripID{
					ID:      fmt.Sprintf("trip_%d", i),
					RouteID: fmt.Sprintf("route_%d", i%100),
				},
			},
		}
	}
	manager.feedVehicles["feed-0"] = feedVehicles
	manager.rebuildMergedRealtimeLocked()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.VehiclesForAgencyID("agency_0")
	}
}
