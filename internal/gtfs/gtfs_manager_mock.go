package gtfs

import (
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// mockTestFeedID is the synthetic feed key used by all Mock* helpers.
// Using a fixed key ensures MockResetRealTimeData can clean up everything.
const mockTestFeedID = "_test"

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
// mockGetOrCreateTestFeed returns the FeedData for mockTestFeedID, creating it if needed.
func (m *Manager) mockGetOrCreateTestFeed() *FeedData {
	m.feedMapMutex.Lock()
	if m.feedData == nil {
		m.feedData = make(map[string]*FeedData)
	}
	feed := m.feedData[mockTestFeedID]
	if feed == nil {
		feed = &FeedData{
			VehicleLastSeen: make(map[string]time.Time),
		}
		m.feedData[mockTestFeedID] = feed
	}
	m.feedMapMutex.Unlock()
	return feed
}

func (m *Manager) MockAddVehicle(vehicleID, tripID, routeID string) {
	feed := m.mockGetOrCreateTestFeed()

	feed.mu.Lock()
	for _, v := range feed.Vehicles {
		if v.ID != nil && v.ID.ID == vehicleID {
			feed.mu.Unlock()
			return
		}
	}
	now := time.Now()
	feed.Vehicles = append(feed.Vehicles, gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: vehicleID},
		Timestamp: &now,
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		},
	})
	feed.mu.Unlock()

	m.buildMergedRealtime()
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
	feed := m.mockGetOrCreateTestFeed()

	feed.mu.Lock()
	for _, v := range feed.Vehicles {
		if v.ID != nil && v.ID.ID == vehicleID {
			feed.mu.Unlock()
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
	feed.Vehicles = append(feed.Vehicles, v)
	feed.mu.Unlock()

	m.buildMergedRealtime()
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
	feed := m.mockGetOrCreateTestFeed()

	feed.mu.Lock()
	feed.Trips = append(feed.Trips, gtfs.Trip{
		ID:              gtfs.TripID{ID: tripID},
		Delay:           delay,
		StopTimeUpdates: stopTimeUpdates,
	})
	feed.mu.Unlock()

	m.buildMergedRealtime()
}

func (m *Manager) MockAddAlert(feedID string, alert gtfs.Alert) {
	m.feedMapMutex.Lock()
	if m.feedData == nil {
		m.feedData = make(map[string]*FeedData)
	}
	feed := m.feedData[feedID]
	if feed == nil {
		feed = &FeedData{
			VehicleLastSeen: make(map[string]time.Time),
		}
		m.feedData[feedID] = feed
	}
	m.feedMapMutex.Unlock()

	feed.mu.Lock()
	feed.Alerts = append(feed.Alerts, alert)
	feed.mu.Unlock()

	m.buildMergedRealtime()
}

// MockResetRealTimeData clears all mock real-time vehicles, trip updates, and alerts
// from the test feed, then rebuilds the merged view.
func (m *Manager) MockResetRealTimeData() {
	m.feedMapMutex.Lock()
	delete(m.feedData, mockTestFeedID)
	m.feedMapMutex.Unlock()

	m.buildMergedRealtime()
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
