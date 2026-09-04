package gtfs

import (
	"context"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

func (m *Manager) MockAddAgency(id, name string) {
	ctx := context.Background()
	// If the agency already exists preserve it so
	// real fields like Timezone are not clobbered.
	if _, err := m.GtfsDB.Queries.GetAgency(ctx, id); err == nil {
		return
	}
	_, _ = m.GtfsDB.Queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       id,
		Name:     name,
		Url:      "",
		Timezone: "",
	})
}

func (m *Manager) MockAddRoute(id, agencyID, name string) {
	ctx := context.Background()
	if _, err := m.GtfsDB.Queries.GetRoute(ctx, id); err == nil {
		return
	}
	_, _ = m.GtfsDB.Queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID:        id,
		AgencyID:  agencyID,
		ShortName: nulls.String(name),
	})
}
func (m *Manager) MockAddVehicle(vehicleID, tripID, routeID string) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	merged := m.mergedRealtime().clone()
	for _, v := range merged.vehicles {
		if v.ID != nil && v.ID.ID == vehicleID {
			return
		}
	}
	now := time.Now()
	merged.vehicles = append(merged.vehicles, gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: vehicleID},
		Timestamp: &now,
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		},
	})

	idx := len(merged.vehicles) - 1
	merged.vehicleLookupByVehicle[vehicleID] = idx
	if tripID != "" {
		merged.vehicleLookupByTrip[tripID] = idx
	}
	m.merged.Store(merged)
}

type MockVehicleOptions struct {
	Position             *gtfs.Position
	CurrentStopSequence  *uint32
	StopID               *string
	CurrentStatus        *gtfs.CurrentStatus
	OccupancyStatus      *gtfs.OccupancyStatus
	ScheduleRelationship gtfs.TripScheduleRelationship // ScheduleRelationship sets the vehicle's trip descriptor schedule relationship (e.g. CANCELED); defaults to SCHEDULED.
	NoTrip               bool                          // NoTrip creates a vehicle with Trip == nil, simulating a GTFS-RT vehicle with no current trip assignment, which VehiclesForAgencyID filters out.
	NoID                 bool                          // NoID creates a vehicle with ID == nil, simulating a GTFS-RT vehicle that omits the vehicle descriptor.
	NoTimestamp          bool                          // NoTimestamp creates a vehicle with Timestamp == nil, simulating a GTFS-RT vehicle with no update time.
	Timestamp            *time.Time                    // Timestamp overrides the vehicle's last-update time; defaults to time.Now() when nil.
}

func (m *Manager) MockAddVehicleWithOptions(vehicleID, tripID, routeID string, opts MockVehicleOptions) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	for _, v := range m.mergedRealtime().vehicles {
		if v.ID != nil && v.ID.ID == vehicleID {
			return
		}
	}
	now := time.Now()
	if opts.Timestamp != nil {
		now = *opts.Timestamp
	}

	var trip *gtfs.Trip
	if !opts.NoTrip {
		trip = &gtfs.Trip{
			ID: gtfs.TripID{
				ID:                   tripID,
				RouteID:              routeID,
				ScheduleRelationship: opts.ScheduleRelationship,
			},
		}
	}

	var vehicleIDPtr *gtfs.VehicleID
	if !opts.NoID {
		vehicleIDPtr = &gtfs.VehicleID{ID: vehicleID}
	}

	var timestamp *time.Time
	if !opts.NoTimestamp {
		timestamp = &now
	}

	v := gtfs.Vehicle{
		ID:                  vehicleIDPtr,
		Timestamp:           timestamp,
		Trip:                trip,
		Position:            opts.Position,
		CurrentStopSequence: opts.CurrentStopSequence,
		StopID:              opts.StopID,
		CurrentStatus:       opts.CurrentStatus,
		OccupancyStatus:     opts.OccupancyStatus,
	}
	merged := m.mergedRealtime().clone()
	merged.vehicles = append(merged.vehicles, v)

	idx := len(merged.vehicles) - 1
	if vehicleID != "" && !opts.NoID {
		merged.vehicleLookupByVehicle[vehicleID] = idx
	}
	if tripID != "" && !opts.NoTrip {
		merged.vehicleLookupByTrip[tripID] = idx
	}
	m.merged.Store(merged)
}

func (m *Manager) MockAddTrip(tripID, agencyID, routeID string) {
	ctx := context.Background()
	_, _ = m.GtfsDB.Queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:        tripID,
		RouteID:   routeID,
		ServiceID: "",
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
	merged := m.mergedRealtime().clone()
	merged.trips = append(merged.trips, trip)
	merged.tripLookup[tripID] = len(merged.trips) - 1
	m.merged.Store(merged)
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

// MockResetRealTimeData clears all mock real-time vehicles, trip updates, and alerts.
func (m *Manager) MockResetRealTimeData() {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	m.feedAlerts = make(map[string][]gtfs.Alert)
	// rebuild republishes an empty snapshot from the (empty) feed maps, which
	// also clears anything the Mock* helpers injected directly.
	m.rebuildMergedRealtimeLocked()
}

// MockAddDuplicatedVehicle adds a vehicle directly to the duplicatedVehicleByRoute map for testing.
func (m *Manager) MockAddDuplicatedVehicle(routeID string, vehicle gtfs.Vehicle) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	merged := m.mergedRealtime().clone()
	merged.duplicatedVehicleByRoute[routeID] = append(merged.duplicatedVehicleByRoute[routeID], vehicle)
	m.merged.Store(merged)
}
