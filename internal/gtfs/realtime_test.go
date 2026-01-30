package gtfs

import (
	"context"
	"sync"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestGetAlertsForRoute(t *testing.T) {
	routeID := "route123"
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		realTimeAlerts: []gtfs.Alert{
			{
				ID: "alert1",
				InformedEntities: []gtfs.AlertInformedEntity{
					{
						RouteID: &routeID,
					},
				},
			},
		},
	}

	alerts := manager.GetAlertsForRoute("route123")

	assert.Len(t, alerts, 1)
	assert.Equal(t, "alert1", alerts[0].ID)
}

func TestGetAlertsForTrip(t *testing.T) {
	tripID := gtfs.TripID{ID: "trip123"}
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		realTimeAlerts: []gtfs.Alert{
			{
				ID: "alert1",
				InformedEntities: []gtfs.AlertInformedEntity{
					{
						TripID: &tripID,
					},
				},
			},
		},
	}

	alerts := manager.GetAlertsForTrip(context.Background(), "trip123")

	assert.Len(t, alerts, 1)
	assert.Equal(t, "alert1", alerts[0].ID)
}

func TestGetAlertsForStop(t *testing.T) {
	stopID := "stop123"
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		realTimeAlerts: []gtfs.Alert{
			{
				ID: "alert1",
				InformedEntities: []gtfs.AlertInformedEntity{
					{
						StopID: &stopID,
					},
				},
			},
		},
	}

	alerts := manager.GetAlertsForStop("stop123")

	assert.Len(t, alerts, 1)
	assert.Equal(t, "alert1", alerts[0].ID)
}

func TestRebuildRealTimeTripLookup(t *testing.T) {
	manager := &Manager{
		realTimeTrips: []gtfs.Trip{
			{
				ID: gtfs.TripID{ID: "trip1"},
			},
			{
				ID: gtfs.TripID{ID: "trip2"},
			},
		},
	}

	rebuildRealTimeTripLookup(manager)

	assert.NotNil(t, manager.realTimeTripLookup)
	assert.Len(t, manager.realTimeTripLookup, 2)
	assert.Equal(t, 0, manager.realTimeTripLookup["trip1"])
	assert.Equal(t, 1, manager.realTimeTripLookup["trip2"])
}

func TestRebuildRealTimeVehicleLookupByTrip(t *testing.T) {
	trip1 := &gtfs.Trip{
		ID: gtfs.TripID{ID: "trip1"},
	}
	trip2 := &gtfs.Trip{
		ID: gtfs.TripID{ID: "trip2"},
	}

	manager := &Manager{
		realTimeVehicles: []gtfs.Vehicle{
			{
				Trip: trip1,
			},
			{
				Trip: trip2,
			},
		},
	}

	rebuildRealTimeVehicleLookupByTrip(manager)

	assert.NotNil(t, manager.realTimeVehicleLookupByTrip)
	assert.Len(t, manager.realTimeVehicleLookupByTrip, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByTrip["trip1"])
	assert.Equal(t, 1, manager.realTimeVehicleLookupByTrip["trip2"])
}

func TestRebuildRealTimeVehicleLookupByVehicle(t *testing.T) {
	vehicleID1 := &gtfs.VehicleID{ID: "vehicle1"}
	vehicleID2 := &gtfs.VehicleID{ID: "vehicle2"}

	manager := &Manager{
		realTimeVehicles: []gtfs.Vehicle{
			{
				ID: vehicleID1,
			},
			{
				ID: vehicleID2,
			},
		},
	}

	rebuildRealTimeVehicleLookupByVehicle(manager)

	assert.NotNil(t, manager.realTimeVehicleLookupByVehicle)
	assert.Len(t, manager.realTimeVehicleLookupByVehicle, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByVehicle["vehicle1"])
	assert.Equal(t, 1, manager.realTimeVehicleLookupByVehicle["vehicle2"])
}

func TestRebuildRealTimeVehicleLookupByVehicle_WithInvalidIDs(t *testing.T) {
	manager := &Manager{
		realTimeVehicles: []gtfs.Vehicle{
			{
				ID: &gtfs.VehicleID{ID: "vehicle1"},
			},
			{
				// Vehicle with nil ID - should be skipped
				ID: nil,
			},
			{
				// Vehicle with empty ID - should be skipped
				ID: &gtfs.VehicleID{ID: ""},
			},
			{
				ID: &gtfs.VehicleID{ID: "vehicle3"},
			},
		},
	}

	filterRealTimeVehicleByValidId(manager)
	rebuildRealTimeVehicleLookupByVehicle(manager)

	assert.NotNil(t, manager.realTimeVehicleLookupByVehicle)
	assert.Len(t, manager.realTimeVehicleLookupByVehicle, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByVehicle["vehicle1"])
	assert.Equal(t, 2, manager.realTimeVehicleLookupByVehicle["vehicle3"])
}
