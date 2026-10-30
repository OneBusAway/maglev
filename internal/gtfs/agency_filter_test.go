package gtfs

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/utils"
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

func routeAgencyMapFor(t *testing.T, manager *Manager, routeIDs []string) map[string]string {
	t.Helper()
	set := make(map[string]struct{}, len(routeIDs))
	for _, id := range routeIDs {
		set[id] = struct{}{}
	}
	got, err := manager.buildRouteAgencyMap(context.Background(), set)
	require.NoError(t, err)
	return got
}

func tripAgencyMapFor(t *testing.T, manager *Manager, trips []gtfs.Trip) map[string]string {
	t.Helper()
	routeIDs := make([]string, 0, len(trips))
	for _, trip := range trips {
		routeIDs = append(routeIDs, trip.ID.RouteID)
	}
	got, err := manager.tripAgencyIDs(context.Background(), trips, routeAgencyMapFor(t, manager, routeIDs))
	require.NoError(t, err)
	return got
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
	filtered, unattributed := filterTripsByAgency(trips, allowed, tripAgencyMapFor(t, manager, trips))

	assert.Len(t, filtered, 2, "should keep only agency-A trips")
	assert.Equal(t, "T1", filtered[0].ID.ID)
	assert.Equal(t, "T3", filtered[1].ID.ID)
	require.Len(t, unattributed, 1, "only the unknown-route trip is unattributable; T2 belongs to agency-B")
	assert.Equal(t, "T4", unattributed[0].ID.ID)
}

// newAttributionTestManager has routes R1 (agency-A) and R2 (agency-C), and
// static trip T1 on R1, for the vehicle attribution tests.
func newAttributionTestManager(t *testing.T) *Manager {
	t.Helper()
	manager := newTestManagerWithRoutes(map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		"R2": {Id: "R2", Agency: &gtfs.Agency{Id: "agency-C"}},
	})
	mustCreateTrip(t, manager, "T1", "R1")
	return manager
}

func vehicleOnTrip(vehicleID, tripID, routeID string) gtfs.Vehicle {
	return gtfs.Vehicle{
		ID:   &gtfs.VehicleID{ID: vehicleID},
		Trip: &gtfs.Trip{ID: gtfs.TripID{ID: tripID, RouteID: routeID}},
	}
}

func TestAttributeVehicles(t *testing.T) {
	tests := []struct {
		name          string
		vehicle       gtfs.Vehicle
		feedAgencyIDs []string
		wantKept      bool
		wantAgency    string
		wantRouteID   string
	}{
		{
			name:        "route agency",
			vehicle:     vehicleOnTrip("V1", "T9", "R1"),
			wantKept:    true,
			wantAgency:  "agency-A",
			wantRouteID: "R1",
		},
		{
			name:        "static trip fills a missing route",
			vehicle:     vehicleOnTrip("V1", "T1", ""),
			wantKept:    true,
			wantAgency:  "agency-A",
			wantRouteID: "R1",
		},
		{
			name:        "unknown feed route resolves through the static trip",
			vehicle:     vehicleOnTrip("V1", "T1", "X"),
			wantKept:    true,
			wantAgency:  "agency-A",
			wantRouteID: "R1",
		},
		{
			name:          "unknown feed route in a feed with agency-ids resolves through the static trip",
			vehicle:       vehicleOnTrip("V1", "T1", "X"),
			feedAgencyIDs: []string{"agency-A"},
			wantKept:      true,
			wantAgency:    "agency-A",
			wantRouteID:   "R1",
		},
		{
			name:          "no trip gets the first configured agency",
			vehicle:       gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "V1"}},
			feedAgencyIDs: []string{"agency-B", "agency-A"},
			wantKept:      true,
			wantAgency:    "agency-B",
		},
		{
			name:          "unresolvable trip gets the first configured agency",
			vehicle:       vehicleOnTrip("V1", "T9", ""),
			feedAgencyIDs: []string{"agency-B"},
			wantKept:      true,
			wantAgency:    "agency-B",
		},
		{
			name:          "unknown route in a feed with agency-ids gets the first configured agency",
			vehicle:       vehicleOnTrip("V1", "T9", "X"),
			feedAgencyIDs: []string{"agency-B", "agency-A"},
			wantKept:      true,
			wantAgency:    "agency-B",
			wantRouteID:   "X",
		},
		{
			name:     "unknown route in a feed without agency-ids is kept without an agency",
			vehicle:  vehicleOnTrip("V1", "T9", "X"),
			wantKept: true,
		},
		{
			name:     "no trip and no configured agency",
			vehicle:  gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "V1"}},
			wantKept: true,
		},
		{
			name:          "another agency's route is filtered out",
			vehicle:       vehicleOnTrip("V2", "T9", "R2"),
			feedAgencyIDs: []string{"agency-A"},
			wantKept:      false,
		},
		{
			name:     "no vehicle ID",
			vehicle:  gtfs.Vehicle{Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}},
			wantKept: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newAttributionTestManager(t)
			feedCfg := RTFeedConfig{ID: "feed", AgencyIDs: tt.feedAgencyIDs}
			manager.feedAgencyFilter = Config{RTFeeds: []RTFeedConfig{{ID: "feed", AgencyIDs: tt.feedAgencyIDs, VehiclePositionsURL: "x", Enabled: true}}}.feedAgencyFilters()

			kept, agencies, err := manager.attributeVehicles(context.Background(), []gtfs.Vehicle{tt.vehicle}, feedCfg)
			require.NoError(t, err)

			if !tt.wantKept {
				assert.Empty(t, kept)
				assert.Empty(t, agencies)
				return
			}
			require.Len(t, kept, 1)
			key := newVehicleKey(kept[0].ID)
			agency, attributed := agencies[key]
			assert.Equal(t, tt.wantAgency != "", attributed)
			assert.Equal(t, tt.wantAgency, agency)
			if tt.wantRouteID != "" {
				assert.Equal(t, tt.wantRouteID, kept[0].Trip.ID.RouteID)
			}
		})
	}
}

func TestAttributeVehiclesDoesNotMutateTheFeedTrip(t *testing.T) {
	manager := newAttributionTestManager(t)
	vehicle := vehicleOnTrip("V1", "T1", "")
	originalTrip := vehicle.Trip

	kept, _, err := manager.attributeVehicles(context.Background(), []gtfs.Vehicle{vehicle}, RTFeedConfig{ID: "feed"})
	require.NoError(t, err)

	require.Len(t, kept, 1)
	assert.Equal(t, "R1", kept[0].Trip.ID.RouteID)
	assert.Empty(t, originalTrip.ID.RouteID, "the parsed feed trip must not be changed in place")
}

func TestAttributeVehiclesLookupError(t *testing.T) {
	manager := newAttributionTestManager(t)
	require.NoError(t, manager.GtfsDB.DB.Close())

	manager.feedAgencyFilter["feed"] = map[string]bool{"agency-A": true}

	_, _, err := manager.attributeVehicles(context.Background(),
		[]gtfs.Vehicle{vehicleOnTrip("V1", "T1", "R1")}, RTFeedConfig{ID: "feed", AgencyIDs: []string{"agency-A"}})
	require.Error(t, err)
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
		{
			name: "blank route id",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{RouteID: strPtr("")},
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    false,
		},
		{
			name: "blank trip route id",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{TripID: &gtfs.TripID{ID: "T1", RouteID: ""}},
				},
			},
			allowed: map[string]bool{"agency-A": true},
			want:    false,
		},
	}

	routeAgencyMap := routeAgencyMapFor(t, manager, []string{"R1", "R2", "R999"})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertMatchesAgency(tt.alert, tt.allowed, routeAgencyMap)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestAgencyFilterFeedAgencyFilterPopulation verifies that feedAgencyFilter is
// correctly populated from RTFeedConfig.AgencyIDs during manager construction.
func TestAgencyFilterFeedAgencyFilterPopulation(t *testing.T) {
	manager := &Manager{
		realTimeMutex:        sync.RWMutex{},
		feedTrips:            make(map[string][]gtfs.Trip),
		feedVehicles:         make(map[string][]gtfs.Vehicle),
		feedAlerts:           make(map[string][]gtfs.Alert),
		feedAgencyFilter:     make(map[string]map[string]bool),
		feedVehicleLastSeen:  make(map[string]map[vehicleKey]time.Time),
		feedVehicleTimestamp: make(map[string]uint64),
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

	filteredA, _ := filterTripsByAgency(tripsA, manager.feedAgencyFilter["feed-a"], tripAgencyMapFor(t, manager, tripsA))
	filteredB, _ := filterTripsByAgency(tripsB, manager.feedAgencyFilter["feed-b"], tripAgencyMapFor(t, manager, tripsB))

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
		// Trip-less vehicles are kept under the feed's first agency.
		if v.Trip != nil {
			assert.Equal(t, targetRouteID, v.Trip.ID.RouteID)
		}
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
	manager.feedVehicleLastSeen["feed"] = map[vehicleKey]time.Time{{id: "V1"}: now}
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	assert.Len(t, manager.GetRealTimeVehicles(), 1, "seeded vehicle should be present")
}

func TestBuildRouteAgencyMap(t *testing.T) {
	t.Run("no route IDs", func(t *testing.T) {
		manager := newTestManager()
		got, err := manager.buildRouteAgencyMap(context.Background(), map[string]struct{}{})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("database unavailable", func(t *testing.T) {
		manager := newTestManager()
		_, err := manager.buildRouteAgencyMap(context.Background(), map[string]struct{}{"R1": {}})
		require.Error(t, err)
	})

	t.Run("query failure", func(t *testing.T) {
		manager := newTestManagerWithRoutes(map[string]*gtfs.Route{
			"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "agency-A"}},
		})
		require.NoError(t, manager.GtfsDB.DB.Close())

		_, err := manager.buildRouteAgencyMap(context.Background(), map[string]struct{}{"R1": {}})
		require.Error(t, err)
	})

	t.Run("more IDs than one SQLite batch", func(t *testing.T) {
		count := utils.IDsPerBatchedQuery + 1
		routes := make(map[string]*gtfs.Route, count)
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("R%d", i)
			routes[id] = &gtfs.Route{Id: id, Agency: &gtfs.Agency{Id: "agency-A"}}
		}
		manager := newTestManagerWithRoutes(routes)

		set := make(map[string]struct{}, count)
		for id := range routes {
			set[id] = struct{}{}
		}
		got, err := manager.buildRouteAgencyMap(context.Background(), set)
		require.NoError(t, err)
		assert.Len(t, got, count)
		assert.Equal(t, "agency-A", got["R0"])
		assert.Equal(t, "agency-A", got[fmt.Sprintf("R%d", count-1)])
	})
}

// positionOnRoute builds a VehiclePosition for a vehicle on the given trip
// and route. An empty tripID and routeID leaves the trip descriptor out.
func positionOnRoute(vehicleID, tripID, routeID string, timestamp time.Time) *gtfsrt.VehiclePosition {
	position := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String(vehicleID)},
		Position:  &gtfsrt.Position{Latitude: proto.Float32(47.6), Longitude: proto.Float32(-122.3)},
		Timestamp: proto.Uint64(uint64(timestamp.Unix())),
	}
	if tripID != "" || routeID != "" {
		position.Trip = &gtfsrt.TripDescriptor{TripId: proto.String(tripID), RouteId: proto.String(routeID)}
	}
	return position
}

// agencyVehicleIDs returns the sorted IDs of the vehicles exported for an agency.
func agencyVehicleIDs(manager *Manager, agencyID string) []string {
	var ids []string
	for _, vehicle := range manager.VehiclePositionsForAgency(agencyID).Vehicles {
		ids = append(ids, vehicle.ID.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestVehicleAgencyIndex(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	positions := []*gtfsrt.VehiclePosition{
		positionOnRoute("V1", "T9", "R1", now),
		positionOnRoute("V2", "T1", "", now),
		positionOnRoute("V3", "T8", "R2", now),
		positionOnRoute("V4", "", "", now),
	}

	t.Run("feed with agency-ids", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		feed := serveVehicleFeed(t, now, positions)
		feed.AgencyIDs = []string{"agency-A"}
		manager.feedAgencyFilter[feed.ID] = map[string]bool{"agency-A": true}

		require.True(t, manager.updateFeedRealtime(context.Background(), feed))

		assert.Equal(t, []string{"V1", "V2", "V4"}, agencyVehicleIDs(manager, "agency-A"))
		assert.Empty(t, agencyVehicleIDs(manager, "agency-C"))
		assert.Len(t, manager.GetRealTimeVehicles(), 3, "V3 runs another agency's route")
	})

	t.Run("feed without agency-ids", func(t *testing.T) {
		manager := newAttributionTestManager(t)

		require.True(t, manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, positions)))

		assert.Equal(t, []string{"V1", "V2"}, agencyVehicleIDs(manager, "agency-A"))
		assert.Equal(t, []string{"V3"}, agencyVehicleIDs(manager, "agency-C"))
		assert.Len(t, manager.GetRealTimeVehicles(), 4, "V4 is kept without an agency")
	})

	t.Run("lookup failure in a feed without agency-ids keeps the vehicles", func(t *testing.T) {
		manager := newTestManager()

		require.True(t, manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, positions)))

		assert.Len(t, manager.GetRealTimeVehicles(), 4)
		assert.Empty(t, agencyVehicleIDs(manager, "agency-A"))
	})

	t.Run("unknown agency", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		require.True(t, manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, positions)))

		assert.Empty(t, manager.VehiclePositionsForAgency("nope").Vehicles)
	})
}

func TestVehicleAgencyIndex_FilledRouteReachesVehiclesForAgency(t *testing.T) {
	tests := []struct {
		name        string
		feedRouteID string
	}{
		{name: "missing route", feedRouteID: ""},
		{name: "unknown route", feedRouteID: "X"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newAttributionTestManager(t)
			now := time.Now().Truncate(time.Second)

			require.True(t, manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{
				positionOnRoute("V2", "T1", tt.feedRouteID, now),
			})))

			vehicles, err := manager.VehiclesForAgencyID(context.Background(), "agency-A")
			require.NoError(t, err)
			require.Len(t, vehicles, 1)
			assert.Equal(t, "V2", vehicles[0].ID.ID)
		})
	}
}

func TestVehicleAgencyIndex_Retention(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	t.Run("a vehicle that left the feed keeps its agency", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now.Add(-time.Minute), []*gtfsrt.VehiclePosition{
			positionOnRoute("V1", "T9", "R1", now.Add(-time.Minute)),
		}))
		manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{
			positionOnRoute("V5", "T8", "R2", now),
		}))

		assert.Equal(t, []string{"V1"}, agencyVehicleIDs(manager, "agency-A"))
		assert.Equal(t, []string{"V5"}, agencyVehicleIDs(manager, "agency-C"))
	})

	t.Run("a stale update keeps the newer record's agency", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now.Add(-time.Minute), []*gtfsrt.VehiclePosition{
			positionOnRoute("V1", "T9", "R1", now),
		}))
		manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{
			positionOnRoute("V1", "T8", "R2", now.Add(-time.Minute)),
		}))

		assert.Equal(t, []string{"V1"}, agencyVehicleIDs(manager, "agency-A"))
		assert.Empty(t, agencyVehicleIDs(manager, "agency-C"))
	})

	t.Run("an expired vehicle leaves the index", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		feed := serveVehicleFeed(t, now.Add(-time.Minute), []*gtfsrt.VehiclePosition{
			positionOnRoute("V1", "T9", "R1", now.Add(-time.Minute)),
		})
		manager.updateFeedRealtime(context.Background(), feed)
		manager.realTimeMutex.Lock()
		manager.feedVehicleLastSeen[feed.ID][vehicleKey{id: "V1"}] = now.Add(-staleVehicleTimeout - time.Minute)
		manager.realTimeMutex.Unlock()

		manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, nil))

		assert.Empty(t, agencyVehicleIDs(manager, "agency-A"))
		assert.Empty(t, manager.feedVehicleAgency[feed.ID])
	})

	t.Run("clearing a feed drops its attribution", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		feed := serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{positionOnRoute("V1", "T9", "R1", now)})
		manager.updateFeedRealtime(context.Background(), feed)

		manager.clearFeedData(feed.ID)

		assert.Empty(t, agencyVehicleIDs(manager, "agency-A"))
		assert.NotContains(t, manager.feedVehicleAgency, feed.ID)
	})
}

func TestVehicleAgencyIndex_TwoFeedsOneAgency(t *testing.T) {
	manager := newAttributionTestManager(t)
	now := time.Now().Truncate(time.Second)

	feedA := serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{positionOnRoute("V1", "T9", "R1", now)})
	feedA.ID = "feed-a"
	feedB := serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{positionOnRoute("V2", "T8", "R1", now)})
	feedB.ID = "feed-b"
	manager.updateFeedRealtime(context.Background(), feedA)
	manager.updateFeedRealtime(context.Background(), feedB)

	assert.Equal(t, []string{"V1", "V2"}, agencyVehicleIDs(manager, "agency-A"))
}

func TestVehicleAgencyIndex_LookupFailureInFilteredFeedSkipsTheUpdate(t *testing.T) {
	manager := newAttributionTestManager(t)
	now := time.Now().Truncate(time.Second)
	feed := serveVehicleFeed(t, now.Add(-time.Minute), []*gtfsrt.VehiclePosition{positionOnRoute("V1", "T9", "R1", now)})
	feed.AgencyIDs = []string{"agency-A"}
	manager.feedAgencyFilter[feed.ID] = map[string]bool{"agency-A": true}
	require.True(t, manager.updateFeedRealtime(context.Background(), feed))
	require.NoError(t, manager.GtfsDB.DB.Close())

	next := serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{positionOnRoute("V2", "T9", "R1", now)})
	next.AgencyIDs = feed.AgencyIDs

	assert.False(t, manager.updateFeedRealtime(context.Background(), next))
	assert.Equal(t, []string{"V1"}, agencyVehicleIDs(manager, "agency-A"), "the previous update stays in place")
}

func TestVehicleAgencyIndex_LookupFailureInUnfilteredFeedKeepsKnownAgencies(t *testing.T) {
	manager := newAttributionTestManager(t)
	now := time.Now().Truncate(time.Second)
	require.True(t, manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now.Add(-time.Minute), []*gtfsrt.VehiclePosition{
		positionOnRoute("V1", "T9", "R1", now.Add(-time.Minute)),
	})))
	require.NoError(t, manager.GtfsDB.DB.Close())

	require.True(t, manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{
		positionOnRoute("V1", "T9", "R1", now),
		positionOnRoute("V2", "T9", "R1", now),
	})))

	assert.Len(t, manager.GetRealTimeVehicles(), 2)
	assert.Equal(t, []string{"V1"}, agencyVehicleIDs(manager, "agency-A"), "V1 keeps its last agency; V2 waits for a successful lookup")
}

func TestAttributeVehiclesStaticTripLookupError(t *testing.T) {
	manager := newAttributionTestManager(t)
	require.NoError(t, manager.GtfsDB.DB.Close())

	// No route_id, so the route lookup is skipped and the static trip lookup fails.
	_, _, err := manager.attributeVehicles(context.Background(), []gtfs.Vehicle{vehicleOnTrip("V1", "T1", "")}, RTFeedConfig{ID: "feed"})
	require.Error(t, err)
}

func TestVehicleAgencyIndex_FollowsAStaticRouteChange(t *testing.T) {
	manager := newAttributionTestManager(t)
	now := time.Now().Truncate(time.Second)
	manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now.Add(-time.Minute), []*gtfsrt.VehiclePosition{
		positionOnRoute("V2", "T1", "", now.Add(-time.Minute)),
	}))
	require.Equal(t, []string{"V2"}, agencyVehicleIDs(manager, "agency-A"))

	_, err := manager.GtfsDB.DB.Exec("UPDATE trips SET route_id = 'R2' WHERE id = 'T1'")
	require.NoError(t, err)
	manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{
		positionOnRoute("V2", "T1", "", now),
	}))

	assert.Empty(t, agencyVehicleIDs(manager, "agency-A"))
	assert.Equal(t, []string{"V2"}, agencyVehicleIDs(manager, "agency-C"))
}

func TestVehicleAgencyIndex_LabelOnlyVehicle(t *testing.T) {
	manager := newAttributionTestManager(t)
	now := time.Now().Truncate(time.Second)
	position := positionOnRoute("", "T9", "R1", now)
	position.Vehicle = &gtfsrt.VehicleDescriptor{Label: proto.String("bus-7")}

	manager.updateFeedRealtime(context.Background(), serveVehicleFeed(t, now, []*gtfsrt.VehiclePosition{position}))

	assert.Equal(t, map[vehicleKey]string{{label: "bus-7"}: "agency-A"}, manager.feedVehicleAgency["test-feed"])
}
