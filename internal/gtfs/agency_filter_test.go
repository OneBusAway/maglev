package gtfs

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

// newTestManagerWithRoutes creates a test manager with an in-memory DB
// pre-populated with the given routes so that route→agency resolution works
// for filtering tests.
func newTestManagerWithRoutes(routes map[string]*gtfs.Route) *Manager {
	m := newTestManager()

	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	if err != nil {
		panic("newTestManagerWithRoutes: failed to create in-memory DB: " + err.Error())
	}
	m.GtfsDB = client

	ctx := context.Background()

	// Collect unique agencies and insert them first (FK constraint).
	seenAgencies := make(map[string]bool)
	for _, route := range routes {
		if route.Agency != nil && !seenAgencies[route.Agency.Id] {
			seenAgencies[route.Agency.Id] = true
			_, _ = client.Queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
				ID:       route.Agency.Id,
				Name:     route.Agency.Name,
				Url:      "",
				Timezone: "",
			})
		}
	}

	// Insert routes.
	for _, route := range routes {
		agencyID := ""
		if route.Agency != nil {
			agencyID = route.Agency.Id
		}
		_, _ = client.Queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
			ID:       route.Id,
			AgencyID: agencyID,
			ShortName: sql.NullString{
				String: route.ShortName,
				Valid:  route.ShortName != "",
			},
		})
	}

	return m
}

// helper to make a *string from a literal
func strPtr(s string) *string { return &s }

func TestFilterTripsByAgency(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
		"R3": {Id: "R3", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},   // agency-A
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}},   // agency-B
		{ID: gtfs.TripID{ID: "T3", RouteID: "R3"}},   // agency-A
		{ID: gtfs.TripID{ID: "T4", RouteID: "R999"}}, // unknown route
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterTripsByAgency(trips, allowed)

	assert.Len(t, filtered, 2, "should keep only agency-A trips")
	assert.Equal(t, "T1", filtered[0].ID.ID)
	assert.Equal(t, "T3", filtered[1].ID.ID)
}

func TestFilterVehiclesByAgency(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
	}
	manager := newTestManagerWithRoutes(routes)

	vehicles := []gtfs.Vehicle{
		{
			ID:   &gtfs.VehicleID{ID: "V1"},
			Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}, // agency-A
		},
		{
			ID:   &gtfs.VehicleID{ID: "V2"},
			Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}}, // agency-B
		},
		{
			ID: &gtfs.VehicleID{ID: "V3"},
			// No trip — should be dropped
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterVehiclesByAgency(vehicles, allowed)

	assert.Len(t, filtered, 1, "only V1 (agency-A) should remain")
	assert.Equal(t, "V1", filtered[0].ID.ID)
}

// TestAgencyFilterNilTrip verifies vehicles without trips are dropped.
func TestAgencyFilterNilTrip(t *testing.T) {
	manager := newTestManagerWithRoutes(map[string]*gtfs.Route{})
	vehicles := []gtfs.Vehicle{
		{ID: &gtfs.VehicleID{ID: "V1"}}, // nil Trip
	}
	allowed := map[string]bool{"any": true}
	assert.Empty(t, manager.filterVehiclesByAgency(vehicles, allowed))
}

// TestAlertMatchesAgency is a table-driven test for the alertMatchesAgency helper.
func TestAlertMatchesAgency(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
	}
	manager := newTestManagerWithRoutes(routes)

	tests := []struct {
		name    string
		alert   gtfs.Alert
		allowed map[string]bool
		want    bool
	}{
		{
			name: "direct agency match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{AgencyID: strPtr("agency-A")},
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    true,
		},
		{
			name: "route-based match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{RouteID: strPtr("R1")},
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    true,
		},
		{
			name: "trip-based match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{TripID: &gtfs.TripID{ID: "T1", RouteID: "R2"}},
				},
			},
			allowed: map[string]bool{"agency-B": true},
			want:    true,
		},
		{
			name: "multiple entities — any match is enough",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{AgencyID: strPtr("agency-B")},
					{RouteID: strPtr("R1")}, // agency-A
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    true,
		},
		{
			name: "no match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{AgencyID: strPtr("agency-C")},
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    false,
		},
		{
			name: "unknown route",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{RouteID: strPtr("R999")},
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    false,
		},
		{
			name:    "empty entities",
			alert:   gtfs.Alert{InformedEntities: []gtfs.AlertInformedEntity{}},
			allowed: map[string]bool{"agency-A": true},
			want:    false,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertMatchesAgencyLocked(ctx, manager, tt.alert, tt.allowed)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestAgencyFilterFeedAgencyFilterPopulation verifies that feedAgencyFilter is
// correctly populated from RTFeedConfig.AgencyIDs during manager construction.
func TestAgencyFilterFeedAgencyFilterPopulation(t *testing.T) {
	manager := &Manager{
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

	// Simulate what InitGTFSManager does for populating feedAgencyFilter
	feeds := []RTFeedConfig{
		{ID: "feed-1", AgencyIDs: []string{"agency-A", "agency-B"}},
		{ID: "feed-2", AgencyIDs: nil},
		{ID: "feed-3", AgencyIDs: []string{}},
		{ID: "feed-4", AgencyIDs: []string{"agency-C"}},
	}
	for _, feedCfg := range feeds {
		if len(feedCfg.AgencyIDs) > 0 {
			filter := make(map[string]bool, len(feedCfg.AgencyIDs))
			for _, id := range feedCfg.AgencyIDs {
				filter[id] = true
			}
			manager.feedAgencyFilter[feedCfg.ID] = filter
		}
	}

	assert.True(t, manager.feedAgencyFilter["feed-1"]["agency-A"])
	assert.True(t, manager.feedAgencyFilter["feed-1"]["agency-B"])
	assert.Nil(t, manager.feedAgencyFilter["feed-2"])
	assert.Nil(t, manager.feedAgencyFilter["feed-3"])
	assert.True(t, manager.feedAgencyFilter["feed-4"]["agency-C"])
}

// TestAgencyFilterMultipleFeedsIntegration verifies that when two feeds are
// configured with different agency filters, each feed's data is filtered
// independently and the merged view contains only the allowed data.
func TestAgencyFilterMultipleFeedsIntegration(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
		"R3": {Id: "R3", Agency: &gtfs.Agency{Id: "agency-C"}},
	}
	manager := newTestManagerWithRoutes(routes)

	manager.feedAgencyFilter["feed-a"] = map[string]bool{"agency-A": true}
	manager.feedAgencyFilter["feed-b"] = map[string]bool{"agency-B": true}

	tripsA := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}, // agency-A ✓
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}}, // agency-B ✗
		{ID: gtfs.TripID{ID: "T3", RouteID: "R3"}}, // agency-C ✗
	}
	tripsB := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T4", RouteID: "R1"}}, // agency-A ✗
		{ID: gtfs.TripID{ID: "T5", RouteID: "R2"}}, // agency-B ✓
	}

	filteredA := manager.filterTripsByAgency(tripsA, manager.feedAgencyFilter["feed-a"])
	filteredB := manager.filterTripsByAgency(tripsB, manager.feedAgencyFilter["feed-b"])

	manager.realTimeMutex.Lock()
	manager.feedTrips["feed-a"] = filteredA
	manager.feedTrips["feed-b"] = filteredB
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	allTrips := manager.GetRealTimeTrips()
	assert.Len(t, allTrips, 2, "merged view should have T1 (agency-A from feed-a) and T5 (agency-B from feed-b)")

	tripIDs := make(map[string]bool)
	for _, trip := range allTrips {
		tripIDs[trip.ID.ID] = true
	}
	assert.True(t, tripIDs["T1"], "T1 (agency-A) should be in merged view")
	assert.True(t, tripIDs["T5"], "T5 (agency-B) should be in merged view")
	assert.False(t, tripIDs["T2"], "T2 (agency-B from feed-a) should be filtered out")
	assert.False(t, tripIDs["T3"], "T3 (agency-C) should be filtered out")
	assert.False(t, tripIDs["T4"], "T4 (agency-A from feed-b) should be filtered out")
}

// TestAgencyFilterIntegration_UpdateFeedRealtime tests the full flow where
// updateFeedRealtime applies agency filtering using real protobuf data.
func TestAgencyFilterIntegration_UpdateFeedRealtime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	// First, fetch without filtering to discover what route IDs appear in the data
	unfilteredManager := newTestManager()
	ctx := context.Background()
	unfilteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:                  "unfiltered",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	})
	allVehicles := unfilteredManager.GetRealTimeVehicles()
	require.NotEmpty(t, allVehicles, "RABA feed should have vehicles")

	routeIDs := make(map[string]bool)
	for _, v := range allVehicles {
		if v.Trip != nil && v.Trip.ID.RouteID != "" {
			routeIDs[v.Trip.ID.RouteID] = true
		}
	}
	if len(routeIDs) == 0 {
		t.Skip("RABA feed has no vehicles with trip/route data — cannot test agency filtering")
	}

	// Pick one route as the target, assign the rest to another agency
	var targetRouteID string
	for rid := range routeIDs {
		targetRouteID = rid
		break
	}

	routes := make(map[string]*gtfs.Route)
	for rid := range routeIDs {
		if rid == targetRouteID {
			routes[rid] = &gtfs.Route{Id: rid, Agency: &gtfs.Agency{Id: "target-agency"}}
		} else {
			routes[rid] = &gtfs.Route{Id: rid, Agency: &gtfs.Agency{Id: "other-agency"}}
		}
	}

	filteredManager := newTestManagerWithRoutes(routes)
	filteredManager.feedAgencyFilter["filtered-feed"] = map[string]bool{"target-agency": true}
	filteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:                  "filtered-feed",
		AgencyIDs:           []string{"target-agency"},
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	})

	filteredVehicles := filteredManager.GetRealTimeVehicles()
	for _, v := range filteredVehicles {
		require.NotNil(t, v.Trip, "filtered vehicle should have a trip")
		assert.Equal(t, targetRouteID, v.Trip.ID.RouteID)
	}
	if len(routeIDs) > 1 {
		assert.Less(t, len(filteredVehicles), len(allVehicles),
			"filtering should reduce vehicle count when multiple routes exist")
	}
	assert.NotEmpty(t, filteredVehicles, "at least one vehicle should match the target agency")
}

// TestAgencyFilterIntegration_NoFilterPassesAll verifies that when no agency
// filter is set, all data flows through unmodified.
func TestAgencyFilterIntegration_NoFilterPassesAll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	manager := newTestManager()
	ctx := context.Background()
	manager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:                  "no-filter",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	})

	assert.NotEmpty(t, manager.GetRealTimeVehicles(), "vehicles should pass through unfiltered")
}

// TestAgencyFilterIntegration_TripUpdates tests filtering of trip updates
// through the full updateFeedRealtime path.
func TestAgencyFilterIntegration_TripUpdates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	ctx := context.Background()

	unfilteredManager := newTestManager()
	unfilteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:              "unfiltered",
		TripUpdatesURL:  server.URL,
		RefreshInterval: 30,
		Enabled:         true,
	})

	allTrips := unfilteredManager.GetRealTimeTrips()
	if len(allTrips) == 0 {
		t.Skip("RABA trip updates feed has no trips — cannot test agency filtering")
	}

	routeIDs := make(map[string]bool)
	for _, trip := range allTrips {
		if trip.ID.RouteID != "" {
			routeIDs[trip.ID.RouteID] = true
		}
	}
	if len(routeIDs) == 0 {
		t.Skip("RABA trips have no route IDs")
	}

	var targetRouteID string
	for rid := range routeIDs {
		targetRouteID = rid
		break
	}

	routes := make(map[string]*gtfs.Route)
	for rid := range routeIDs {
		if rid == targetRouteID {
			routes[rid] = &gtfs.Route{Id: rid, Agency: &gtfs.Agency{Id: "target-agency"}}
		} else {
			routes[rid] = &gtfs.Route{Id: rid, Agency: &gtfs.Agency{Id: "other-agency"}}
		}
	}

	filteredManager := newTestManagerWithRoutes(routes)
	filteredManager.feedAgencyFilter["filtered"] = map[string]bool{"target-agency": true}
	filteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:              "filtered",
		AgencyIDs:       []string{"target-agency"},
		TripUpdatesURL:  server.URL,
		RefreshInterval: 30,
		Enabled:         true,
	})

	filteredTrips := filteredManager.GetRealTimeTrips()
	for _, trip := range filteredTrips {
		assert.Equal(t, targetRouteID, trip.ID.RouteID,
			"all filtered trips should belong to the target route")
	}
	if len(routeIDs) > 1 {
		assert.Less(t, len(filteredTrips), len(allTrips),
			"filtering should reduce trip count when multiple routes exist")
	}
	assert.NotEmpty(t, filteredTrips, "at least one trip should match the target agency")
}

// TestFeedVehicleRetentionWithAgencyFilter ensures that the stale vehicle
// retention logic still works correctly when agency filtering is active.
func TestFeedVehicleRetentionWithAgencyFilter(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	manager.feedAgencyFilter["feed"] = map[string]bool{"agency-A": true}

	now := time.Now()
	vid := &gtfs.VehicleID{ID: "V1"}
	ts := now.Add(-1 * time.Minute)

	manager.realTimeMutex.Lock()
	manager.feedVehicles["feed"] = []gtfs.Vehicle{
		{ID: vid, Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "R1"}}, Timestamp: &ts},
	}
	manager.feedVehicleLastSeen["feed"] = map[string]time.Time{"V1": now}
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	assert.Len(t, manager.GetRealTimeVehicles(), 1, "seeded vehicle should be present")
}
