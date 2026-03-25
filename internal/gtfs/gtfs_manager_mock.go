package gtfs

import (
	"time"

	"github.com/OneBusAway/go-gtfs"
)

func (m *Manager) MockAddAgency(id, name string) {
	for _, a := range m.gtfsData.Agencies {
		if a.Id == id {
			return
		}
	}
	m.gtfsData.Agencies = append(m.gtfsData.Agencies, gtfs.Agency{
		Id:   id,
		Name: name,
	})
}

func (m *Manager) MockAddRoute(id, agencyID, name string) {
	for _, r := range m.gtfsData.Routes {
		if r.Id == id {
			return
		}
	}
	m.gtfsData.Routes = append(m.gtfsData.Routes, gtfs.Route{
		Id:        id,
		Agency:    &gtfs.Agency{Id: agencyID},
		ShortName: name,
	})
}
func (m *Manager) MockAddVehicle(vehicleID, tripID, routeID string) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	for _, v := range m.realTimeVehicles {
		if v.ID != nil && v.ID.ID == vehicleID {
			return
		}
	}
	now := time.Now()
	m.realTimeVehicles = append(m.realTimeVehicles, gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: vehicleID},
		Timestamp: &now,
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		},
	})

	idx := len(m.realTimeVehicles) - 1
	m.realTimeVehicleLookupByVehicle[vehicleID] = idx
	if tripID != "" {
		m.realTimeVehicleLookupByTrip[tripID] = idx
	}
}

type MockVehicleOptions struct {
	Position            *gtfs.Position
	CurrentStopSequence *uint32
	StopID              *string
	CurrentStatus       *gtfs.CurrentStatus
	OccupancyStatus     *gtfs.OccupancyStatus
	NoTrip              bool // NoTrip creates a vehicle with Trip == nil, simulating a GTFS-RT vehicle with no current trip assignment, which VehiclesForAgencyID filters out.
	NoID                bool // NoID creates a vehicle with ID == nil, simulating a GTFS-RT vehicle that omits the vehicle descriptor.
}

func (m *Manager) MockAddVehicleWithOptions(vehicleID, tripID, routeID string, opts MockVehicleOptions) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	for _, v := range m.realTimeVehicles {
		if v.ID != nil && v.ID.ID == vehicleID {
			return
		}
	}
	now := time.Now()

	var trip *gtfs.Trip
	if !opts.NoTrip {
		trip = &gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		}
	}

	var vehicleIDPtr *gtfs.VehicleID
	if !opts.NoID {
		vehicleIDPtr = &gtfs.VehicleID{ID: vehicleID}
	}

	v := gtfs.Vehicle{
		ID:                  vehicleIDPtr,
		Timestamp:           &now,
		Trip:                trip,
		Position:            opts.Position,
		CurrentStopSequence: opts.CurrentStopSequence,
		StopID:              opts.StopID,
		CurrentStatus:       opts.CurrentStatus,
		OccupancyStatus:     opts.OccupancyStatus,
	}
	m.realTimeVehicles = append(m.realTimeVehicles, v)

	idx := len(m.realTimeVehicles) - 1
	if vehicleID != "" && !opts.NoID {
		m.realTimeVehicleLookupByVehicle[vehicleID] = idx
	}
	if tripID != "" && !opts.NoTrip {
		m.realTimeVehicleLookupByTrip[tripID] = idx
	}
}

func (m *Manager) MockAddTrip(tripID, agencyID, routeID string) {
	for _, t := range m.gtfsData.Trips {
		if t.ID == tripID {
			return
		}
	}
	m.gtfsData.Trips = append(m.gtfsData.Trips, gtfs.ScheduledTrip{
		ID:    tripID,
		Route: &gtfs.Route{Id: routeID},
	})
}

func (m *Manager) MockAddTripUpdate(tripID string, delay *time.Duration, stopTimeUpdates []gtfs.StopTimeUpdate) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	trip := gtfs.Trip{
		ID:              gtfs.TripID{ID: tripID},
		Delay:           delay,
		StopTimeUpdates: stopTimeUpdates,
	}
	m.realTimeTrips = append(m.realTimeTrips, trip)
	if m.realTimeTripLookup == nil {
		m.realTimeTripLookup = make(map[string]int)
	}
	m.realTimeTripLookup[tripID] = len(m.realTimeTrips) - 1
}

func (m *Manager) MockAddAlert(feedID string, alert gtfs.Alert) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	if m.feedAlerts == nil {
		m.feedAlerts = make(map[string][]gtfs.Alert)
	}
	m.feedAlerts[feedID] = append(m.feedAlerts[feedID], alert)
	m.rebuildMergedRealtimeLocked()
}

// MockResetRealTimeData clears all mock real-time vehicles and trip updates.
func (m *Manager) MockResetRealTimeData() {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	m.realTimeVehicles = nil
	m.realTimeVehicleLookupByVehicle = make(map[string]int)
	m.realTimeVehicleLookupByTrip = make(map[string]int)
	m.duplicatedVehicleByRoute = make(map[string][]gtfs.Vehicle)
	m.realTimeTrips = nil
	m.realTimeTripLookup = make(map[string]int)
}

// MockClearServiceIDsCache evicts all entries from the active-service-IDs cache.
// Call this in tests that mutate the calendar tables of a shared Manager to ensure
// the next request re-queries the database with the updated data.
//
// Safe to call without holding staticMutex. Acquires only activeServiceIDsCacheMutex,
// consistent with the lock ordering: staticMutex → activeServiceIDsCacheMutex.
func (m *Manager) MockClearServiceIDsCache() {
	m.activeServiceIDsCacheMutex.Lock()
	m.activeServiceIDsCache = make(map[string][]string)
	m.cacheEpoch.Add(1)
	m.activeServiceIDsCacheMutex.Unlock()
}
