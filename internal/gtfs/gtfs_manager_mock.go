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
	m.MockAddVehicleWithOptions(vehicleID, tripID, routeID, MockVehicleOptions{})
}

type MockVehicleOptions struct {
	Position            *gtfs.Position
	CurrentStopSequence *uint32
	StopID              *string
	CurrentStatus       *gtfs.CurrentStatus
	OccupancyStatus     *gtfs.OccupancyStatus
	NoTrip              bool       // NoTrip creates a vehicle with Trip == nil, simulating a GTFS-RT vehicle with no current trip assignment.
	NoID                bool       // NoID creates a vehicle with ID == nil, simulating a GTFS-RT vehicle that omits the vehicle descriptor.
	NoTimestamp         bool       // NoTimestamp creates a vehicle with Timestamp == nil, simulating a GTFS-RT vehicle with no update time.
	Timestamp           *time.Time // Timestamp overrides the vehicle's last-update time; defaults to time.Now() when nil.
	FeedID              string     // FeedID assigns the vehicle to a specific feed; defaults to "feed-0".
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
	if opts.Timestamp != nil {
		now = *opts.Timestamp
	}

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

	// Store per-feed and rebuild the merged view so lookups match production.
	feedID := opts.FeedID
	if feedID == "" {
		feedID = "feed-0"
	}
	m.feedVehicles[feedID] = append(m.feedVehicles[feedID], v)
	m.rebuildMergedRealtimeLocked()
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

// MockSetFeedAgencyFilter assigns the set of agency IDs served by a feed, so
// trip-less vehicles on that feed resolve to those agencies.
func (m *Manager) MockSetFeedAgencyFilter(feedID string, agencyIDs ...string) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	filter := make(map[string]bool, len(agencyIDs))
	for _, id := range agencyIDs {
		filter[id] = true
	}
	if m.feedAgencyFilter == nil {
		m.feedAgencyFilter = make(map[string]map[string]bool)
	}
	m.feedAgencyFilter[feedID] = filter
}

// MockResetRealTimeData clears all mock real-time vehicles, trip updates, and alerts.
func (m *Manager) MockResetRealTimeData() {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()

	m.realTimeVehicles = nil
	m.realTimeVehicleLookupByVehicle = make(map[string]int)
	m.realTimeVehicleLookupByTrip = make(map[string]int)
	m.duplicatedVehicleByRoute = make(map[string][]gtfs.Vehicle)
	m.realTimeTrips = nil
	m.realTimeTripLookup = make(map[string]int)
	m.feedVehicles = make(map[string][]gtfs.Vehicle)
	m.feedAlerts = make(map[string][]gtfs.Alert)
	m.rebuildMergedRealtimeLocked()
}
