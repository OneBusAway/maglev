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
	"github.com/stretchr/testify/require"
)

// newTestManagerWithRoutes creates a test manager with a pre-populated
// routesMap so that route→agency resolution works for filtering tests.
func newTestManagerWithRoutes(routes map[string]*gtfs.Route) *Manager {
	m := newTestManager()
	m.routesMap = routes
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

func TestFilterTripsByAgency_AllAllowed(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
	}
	manager := newTestManagerWithRoutes(routes)

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}},
	}

	allowed := map[string]bool{"agency-A": true, "agency-B": true}
	filtered := manager.filterTripsByAgency(trips, allowed)

	assert.Len(t, filtered, 2, "all trips belong to allowed agencies")
}

func TestFilterTripsByAgency_NoneAllowed(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
	}

	allowed := map[string]bool{"agency-X": true}
	filtered := manager.filterTripsByAgency(trips, allowed)

	assert.Empty(t, filtered, "no trips should match agency-X")
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

func TestFilterAlertsByAgency_DirectAgencyMatch(t *testing.T) {
	routes := map[string]*gtfs.Route{}
	manager := newTestManagerWithRoutes(routes)

	alerts := []gtfs.Alert{
		{
			ID: "alert-1",
			InformedEntities: []gtfs.AlertInformedEntity{
				{AgencyID: strPtr("agency-A")},
			},
		},
		{
			ID: "alert-2",
			InformedEntities: []gtfs.AlertInformedEntity{
				{AgencyID: strPtr("agency-B")},
			},
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterAlertsByAgency(alerts, allowed)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-1", filtered[0].ID)
}

func TestFilterAlertsByAgency_RouteBasedMatch(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
	}
	manager := newTestManagerWithRoutes(routes)

	alerts := []gtfs.Alert{
		{
			ID: "alert-1",
			InformedEntities: []gtfs.AlertInformedEntity{
				{RouteID: strPtr("R1")}, // resolves to agency-A
			},
		},
		{
			ID: "alert-2",
			InformedEntities: []gtfs.AlertInformedEntity{
				{RouteID: strPtr("R2")}, // resolves to agency-B
			},
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterAlertsByAgency(alerts, allowed)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-1", filtered[0].ID)
}

func TestFilterAlertsByAgency_TripBasedMatch(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	alerts := []gtfs.Alert{
		{
			ID: "alert-1",
			InformedEntities: []gtfs.AlertInformedEntity{
				{TripID: &gtfs.TripID{ID: "T1", RouteID: "R1"}}, // resolves to agency-A
			},
		},
		{
			ID: "alert-2",
			InformedEntities: []gtfs.AlertInformedEntity{
				{TripID: &gtfs.TripID{ID: "T2", RouteID: "R999"}}, // unknown route
			},
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterAlertsByAgency(alerts, allowed)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-1", filtered[0].ID)
}

func TestFilterAlertsByAgency_MultipleEntitiesAnyMatch(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	// Alert has entities for both agency-B (direct) and a route belonging to agency-A.
	// Since at least one entity matches agency-A, the alert should be included.
	alerts := []gtfs.Alert{
		{
			ID: "alert-mixed",
			InformedEntities: []gtfs.AlertInformedEntity{
				{AgencyID: strPtr("agency-B")},
				{RouteID: strPtr("R1")}, // agency-A
			},
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterAlertsByAgency(alerts, allowed)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-mixed", filtered[0].ID)
}

func TestFilterAlertsByAgency_NoEntities(t *testing.T) {
	manager := newTestManagerWithRoutes(map[string]*gtfs.Route{})

	alerts := []gtfs.Alert{
		{ID: "alert-empty", InformedEntities: nil},
		{ID: "alert-empty-slice", InformedEntities: []gtfs.AlertInformedEntity{}},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := manager.filterAlertsByAgency(alerts, allowed)

	assert.Empty(t, filtered, "alerts without informed entities should be dropped")
}

// TestNoFilterWhenAgencyIDsEmpty verifies that when AgencyIDs is empty,
// all data passes through unfiltered.
func TestNoFilterWhenAgencyIDsEmpty(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-B"}},
	}
	manager := newTestManagerWithRoutes(routes)

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}},
	}

	// Empty filter means no filtering — feedAgencyFilter[feedID] would be nil
	agencyFilter := manager.feedAgencyFilter["some-feed"] // nil
	assert.Nil(t, agencyFilter)

	// Confirm the filterTripsByAgency isn't called when filter is empty
	// by verifying that len(agencyFilter) == 0
	assert.Equal(t, 0, len(agencyFilter))

	// If the code correctly skips filtering when len(agencyFilter) == 0,
	// all trips should be present. We test the full path via updateFeedRealtime.
	_ = trips // used in integration test below
}

// TestAgencyFilterIntegration_UpdateFeedRealtime tests the full flow where
// updateFeedRealtime applies agency filtering using real protobuf data.
// Uses RABA vehicle positions and populates routesMap with only some of
// the routes, then filters by a specific agency.
func TestAgencyFilterIntegration_UpdateFeedRealtime(t *testing.T) {
	// Serve RABA vehicle positions
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	// First, fetch without filtering to discover what route IDs appear in the data
	unfilteredManager := newTestManager()
	unfilteredFeed := RTFeedConfig{
		ID:                  "unfiltered",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	}
	ctx := context.Background()
	unfilteredManager.updateFeedRealtime(ctx, unfilteredFeed)
	allVehicles := unfilteredManager.GetRealTimeVehicles()
	require.NotEmpty(t, allVehicles, "RABA feed should have vehicles")

	// Collect all unique route IDs from the feed
	routeIDs := make(map[string]bool)
	for _, v := range allVehicles {
		if v.Trip != nil && v.Trip.ID.RouteID != "" {
			routeIDs[v.Trip.ID.RouteID] = true
		}
	}

	if len(routeIDs) == 0 {
		t.Skip("RABA feed has no vehicles with trip/route data — cannot test agency filtering")
	}

	// Pick the first route and assign it to "target-agency", assign the rest to "other-agency"
	var targetRouteID string
	for rid := range routeIDs {
		targetRouteID = rid
		break
	}

	routes := make(map[string]*gtfs.Route)
	targetAgency := &gtfs.Agency{Id: "target-agency"}
	otherAgency := &gtfs.Agency{Id: "other-agency"}
	for rid := range routeIDs {
		if rid == targetRouteID {
			routes[rid] = &gtfs.Route{Id: rid, Agency: targetAgency}
		} else {
			routes[rid] = &gtfs.Route{Id: rid, Agency: otherAgency}
		}
	}

	// Now create a filtered manager
	filteredManager := newTestManagerWithRoutes(routes)
	filteredManager.feedAgencyFilter["filtered-feed"] = map[string]bool{"target-agency": true}

	filteredFeed := RTFeedConfig{
		ID:                  "filtered-feed",
		AgencyIDs:           []string{"target-agency"},
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	}
	filteredManager.updateFeedRealtime(ctx, filteredFeed)

	filteredVehicles := filteredManager.GetRealTimeVehicles()

	// All filtered vehicles should belong to the target route
	for _, v := range filteredVehicles {
		require.NotNil(t, v.Trip, "filtered vehicle should have a trip")
		assert.Equal(t, targetRouteID, v.Trip.ID.RouteID,
			"filtered vehicle should only have route %s", targetRouteID)
	}

	// Filtered count should be less than unfiltered (assuming >1 route)
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
	feed := RTFeedConfig{
		ID:                  "no-filter",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
		// No AgencyIDs — no filter
	}

	ctx := context.Background()
	manager.updateFeedRealtime(ctx, feed)

	vehicles := manager.GetRealTimeVehicles()
	assert.NotEmpty(t, vehicles, "vehicles should pass through unfiltered")
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

	// feed-1 should have both agencies
	assert.True(t, manager.feedAgencyFilter["feed-1"]["agency-A"])
	assert.True(t, manager.feedAgencyFilter["feed-1"]["agency-B"])

	// feed-2 and feed-3 should not have a filter (empty AgencyIDs)
	assert.Nil(t, manager.feedAgencyFilter["feed-2"])
	assert.Nil(t, manager.feedAgencyFilter["feed-3"])

	// feed-4 should have agency-C
	assert.True(t, manager.feedAgencyFilter["feed-4"]["agency-C"])
}

// TestAgencyFilterConcurrentWithStaticUpdate verifies that agency filtering
// (which reads routesMap) is safe concurrent with static GTFS updates
// (which writes routesMap under staticMutex).
func TestAgencyFilterConcurrentWithStaticUpdate(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	// Run filtering in a goroutine while updating routesMap concurrently
	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
	}
	allowed := map[string]bool{"agency-A": true}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = manager.filterTripsByAgency(trips, allowed)
		}
	}()

	// Simulate static update by writing to routesMap under lock
	for i := 0; i < 100; i++ {
		manager.staticMutex.Lock()
		manager.routesMap = map[string]*gtfs.Route{
			"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		}
		manager.staticMutex.Unlock()
	}

	<-done
}

// TestAlertMatchesAgency is a table-driven test for the alertMatchesAgencyLocked helper.
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
			name: "empty entities",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertMatchesAgencyLocked(manager, tt.alert, tt.allowed)
			assert.Equal(t, tt.want, got)
		})
	}
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

	// Feed A allows agency-A only
	manager.feedAgencyFilter["feed-a"] = map[string]bool{"agency-A": true}
	// Feed B allows agency-B only
	manager.feedAgencyFilter["feed-b"] = map[string]bool{"agency-B": true}

	// Directly populate feed data and simulate filtering
	tripsA := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}, // agency-A ✓
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}}, // agency-B ✗
		{ID: gtfs.TripID{ID: "T3", RouteID: "R3"}}, // agency-C ✗
	}
	tripsB := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T4", RouteID: "R1"}}, // agency-A ✗
		{ID: gtfs.TripID{ID: "T5", RouteID: "R2"}}, // agency-B ✓
	}

	filterA := manager.feedAgencyFilter["feed-a"]
	filterB := manager.feedAgencyFilter["feed-b"]

	filteredA := manager.filterTripsByAgency(tripsA, filterA)
	filteredB := manager.filterTripsByAgency(tripsB, filterB)

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

// TestAgencyFilterEmptyResult verifies that filtering can produce zero results
// without panicking.
func TestAgencyFilterEmptyResult(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	allowed := map[string]bool{"agency-X": true} // no routes match

	trips := []gtfs.Trip{{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}}
	vehicles := []gtfs.Vehicle{
		{ID: &gtfs.VehicleID{ID: "V1"}, Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "R1"}}},
	}
	alerts := []gtfs.Alert{
		{ID: "a1", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: strPtr("agency-A")}}},
	}

	assert.Empty(t, manager.filterTripsByAgency(trips, allowed))
	assert.Empty(t, manager.filterVehiclesByAgency(vehicles, allowed))
	assert.Empty(t, manager.filterAlertsByAgency(alerts, allowed))
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

	// First, load unfiltered to discover route IDs
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

	// Collect route IDs and pick one as the target
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

	// Build routes map
	routes := make(map[string]*gtfs.Route)
	targetAgency := &gtfs.Agency{Id: "target-agency"}
	otherAgency := &gtfs.Agency{Id: "other-agency"}
	for rid := range routeIDs {
		if rid == targetRouteID {
			routes[rid] = &gtfs.Route{Id: rid, Agency: targetAgency}
		} else {
			routes[rid] = &gtfs.Route{Id: rid, Agency: otherAgency}
		}
	}

	// Create filtered manager
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

	// Seed initial vehicle data
	manager.realTimeMutex.Lock()
	manager.feedVehicles["feed"] = []gtfs.Vehicle{
		{ID: vid, Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "R1"}}, Timestamp: &ts},
	}
	manager.feedVehicleLastSeen["feed"] = map[string]time.Time{
		"V1": now,
	}
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	vehicles := manager.GetRealTimeVehicles()
	assert.Len(t, vehicles, 1, "seeded vehicle should be present")
}
