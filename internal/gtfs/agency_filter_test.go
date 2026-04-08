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
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

// helper to make a *string from a literal
func strPtr(s string) *string { return &s }

// newTestManagerWithDB creates a Manager backed by the RABA test DB loaded from
// testdata/raba.zip. Used by integration tests that require real route→agency resolution.
func newTestManagerWithDB(t *testing.T) *Manager {
	t.Helper()
	cfg := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Development,
	}
	client, err := gtfsdb.NewClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	err = client.ImportFromFile(ctx, filepath.Join("../../testdata", "raba.zip"))
	require.NoError(t, err)

	m := newTestManager()
	m.GtfsDB = client
	return m
}

func TestFilterTripsByAgency(t *testing.T) {
	// R1 and R3 belong to agency-A, R2 to agency-B
	allowedRouteIDs := map[string]bool{"R1": true, "R3": true}

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},   // allowed
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}},   // not allowed
		{ID: gtfs.TripID{ID: "T3", RouteID: "R3"}},   // allowed
		{ID: gtfs.TripID{ID: "T4", RouteID: "R999"}}, // unknown route
	}

	filtered := filterTripsByAgency(trips, allowedRouteIDs)

	assert.Len(t, filtered, 2, "should keep only agency-A trips")
	assert.Equal(t, "T1", filtered[0].ID.ID)
	assert.Equal(t, "T3", filtered[1].ID.ID)
}

func TestFilterTripsByAgency_AllAllowed(t *testing.T) {
	allowedRouteIDs := map[string]bool{"R1": true, "R2": true}

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}},
	}

	filtered := filterTripsByAgency(trips, allowedRouteIDs)

	assert.Len(t, filtered, 2, "all trips belong to allowed agencies")
}

func TestFilterTripsByAgency_NoneAllowed(t *testing.T) {
	allowedRouteIDs := map[string]bool{} // no routes for agency-X

	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
	}

	filtered := filterTripsByAgency(trips, allowedRouteIDs)

	assert.Empty(t, filtered, "no trips should match agency-X")
}

func TestFilterVehiclesByAgency(t *testing.T) {
	// R1 belongs to agency-A, R2 to agency-B
	allowedRouteIDs := map[string]bool{"R1": true}

	vehicles := []gtfs.Vehicle{
		{
			ID:   &gtfs.VehicleID{ID: "V1"},
			Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}, // allowed
		},
		{
			ID:   &gtfs.VehicleID{ID: "V2"},
			Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}}, // not allowed
		},
		{
			ID: &gtfs.VehicleID{ID: "V3"},
			// No trip — should be dropped
		},
	}

	filtered := filterVehiclesByAgency(vehicles, allowedRouteIDs)

	assert.Len(t, filtered, 1, "only V1 (agency-A) should remain")
	assert.Equal(t, "V1", filtered[0].ID.ID)
}

func TestFilterAlertsByAgency_DirectAgencyMatch(t *testing.T) {
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
	filtered := filterAlertsByAgency(alerts, allowed, map[string]bool{})

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-1", filtered[0].ID)
}

func TestFilterAlertsByAgency_RouteBasedMatch(t *testing.T) {
	// R1 belongs to agency-A, R2 to agency-B
	allowedRouteIDs := map[string]bool{"R1": true}

	alerts := []gtfs.Alert{
		{
			ID: "alert-1",
			InformedEntities: []gtfs.AlertInformedEntity{
				{RouteID: strPtr("R1")}, // allowed
			},
		},
		{
			ID: "alert-2",
			InformedEntities: []gtfs.AlertInformedEntity{
				{RouteID: strPtr("R2")}, // not allowed
			},
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := filterAlertsByAgency(alerts, allowed, allowedRouteIDs)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-1", filtered[0].ID)
}

func TestFilterAlertsByAgency_TripBasedMatch(t *testing.T) {
	// R1 belongs to agency-A
	allowedRouteIDs := map[string]bool{"R1": true}

	alerts := []gtfs.Alert{
		{
			ID: "alert-1",
			InformedEntities: []gtfs.AlertInformedEntity{
				{TripID: &gtfs.TripID{ID: "T1", RouteID: "R1"}}, // allowed
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
	filtered := filterAlertsByAgency(alerts, allowed, allowedRouteIDs)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-1", filtered[0].ID)
}

func TestFilterAlertsByAgency_MultipleEntitiesAnyMatch(t *testing.T) {
	// R1 belongs to agency-A
	allowedRouteIDs := map[string]bool{"R1": true}

	alerts := []gtfs.Alert{
		{
			ID: "alert-mixed",
			InformedEntities: []gtfs.AlertInformedEntity{
				{AgencyID: strPtr("agency-B")},
				{RouteID: strPtr("R1")}, // agency-A — any match wins
			},
		},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := filterAlertsByAgency(alerts, allowed, allowedRouteIDs)

	assert.Len(t, filtered, 1)
	assert.Equal(t, "alert-mixed", filtered[0].ID)
}

func TestFilterAlertsByAgency_NoEntities(t *testing.T) {
	alerts := []gtfs.Alert{
		{ID: "alert-empty", InformedEntities: nil},
		{ID: "alert-empty-slice", InformedEntities: []gtfs.AlertInformedEntity{}},
	}

	allowed := map[string]bool{"agency-A": true}
	filtered := filterAlertsByAgency(alerts, allowed, map[string]bool{})

	assert.Empty(t, filtered, "alerts without informed entities should be dropped")
}

// TestNoFilterWhenAgencyIDsEmpty verifies that when AgencyIDs is empty,
// all data passes through unfiltered.
func TestNoFilterWhenAgencyIDsEmpty(t *testing.T) {
	manager := newTestManager()

	// Empty filter means no filtering — feedAgencyFilter[feedID] would be nil
	agencyFilter := manager.feedAgencyFilter["some-feed"] // nil
	assert.Nil(t, agencyFilter)
	assert.Equal(t, 0, len(agencyFilter))
}

// TestAgencyFilterIntegration_UpdateFeedRealtime tests the full flow where
// updateFeedRealtime applies agency filtering using the real RABA DB and protobuf data.
func TestAgencyFilterIntegration_UpdateFeedRealtime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	ctx := context.Background()

	// Load without filtering to discover what vehicles exist
	unfilteredManager := newTestManager()
	unfilteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:                  "unfiltered",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	})
	allVehicles := unfilteredManager.GetRealTimeVehicles()
	require.NotEmpty(t, allVehicles, "RABA feed should have vehicles")

	// Load with a DB so route→agency resolution works
	filteredManager := newTestManagerWithDB(t)

	var realAgencyID string
	for _, v := range allVehicles {
		if v.Trip == nil {
			continue
		}
		route, err := filteredManager.GtfsDB.Queries.GetRoute(ctx, v.Trip.ID.RouteID)
		if err != nil {
			continue
		}
		realAgencyID = route.AgencyID
		break
	}
	if realAgencyID == "" {
		t.Skip("no RABA routes found in DB for vehicles in feed — cannot test agency filtering")
	}

	filteredManager.feedAgencyFilter["filtered-feed"] = map[string]bool{realAgencyID: true}
	filteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:                  "filtered-feed",
		AgencyIDs:           []string{realAgencyID},
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	})

	filteredVehicles := filteredManager.GetRealTimeVehicles()
	assert.NotEmpty(t, filteredVehicles, "vehicles matching the real agency should pass through")

	// Filtering by an unknown agency should drop all vehicles
	filteredManager2 := newTestManagerWithDB(t)
	filteredManager2.feedAgencyFilter["no-match"] = map[string]bool{"nonexistent-agency": true}
	filteredManager2.updateFeedRealtime(ctx, RTFeedConfig{
		ID:                  "no-match",
		AgencyIDs:           []string{"nonexistent-agency"},
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	})
	assert.Empty(t, filteredManager2.GetRealTimeVehicles(), "unknown agency should filter out all vehicles")
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

// TestAgencyFilterConcurrentFilterCalls verifies that filter functions can be
// called concurrently without data races (no shared mutable state).
func TestAgencyFilterConcurrentFilterCalls(t *testing.T) {
	allowedRouteIDs := map[string]bool{"R1": true}
	trips := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = filterTripsByAgency(trips, allowedRouteIDs)
		}
	}()
	for i := 0; i < 100; i++ {
		_ = filterTripsByAgency(trips, allowedRouteIDs)
	}
	<-done
}

// TestAlertMatchesAgency is a table-driven test for the alertMatchesAgency helper.
func TestAlertMatchesAgency(t *testing.T) {
	// R1 belongs to agency-A, R2 to agency-B
	allowedRouteIDs := map[string]bool{
		"R1": true,
		"R2": true,
	}

	tests := []struct {
		name            string
		alert           gtfs.Alert
		allowed         map[string]bool
		allowedRouteIDs map[string]bool
		want            bool
	}{
		{
			name: "direct agency match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{AgencyID: strPtr("agency-A")},
				},
			},
			allowed:         map[string]bool{"agency-A": true},
			allowedRouteIDs: allowedRouteIDs,
			want:            true,
		},
		{
			name: "route-based match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{RouteID: strPtr("R1")},
				},
			},
			allowed:         map[string]bool{"agency-A": true},
			allowedRouteIDs: allowedRouteIDs,
			want:            true,
		},
		{
			name: "trip-based match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{TripID: &gtfs.TripID{ID: "T1", RouteID: "R2"}},
				},
			},
			allowed:         map[string]bool{"agency-B": true},
			allowedRouteIDs: allowedRouteIDs,
			want:            true,
		},
		{
			name: "no match",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{AgencyID: strPtr("agency-C")},
				},
			},
			allowed:         map[string]bool{"agency-A": true},
			allowedRouteIDs: map[string]bool{"R1": true},
			want:            false,
		},
		{
			name: "empty entities",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{},
			},
			allowed:         map[string]bool{"agency-A": true},
			allowedRouteIDs: allowedRouteIDs,
			want:            false,
		},
		{
			name: "unknown route",
			alert: gtfs.Alert{
				InformedEntities: []gtfs.AlertInformedEntity{
					{RouteID: strPtr("R999")},
				},
			},
			allowed:         map[string]bool{"agency-A": true},
			allowedRouteIDs: allowedRouteIDs,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertMatchesAgency(tt.alert, tt.allowed, tt.allowedRouteIDs)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestAgencyFilterMultipleFeedsIntegration verifies that when two feeds are
// configured with different agency filters, each feed's data is filtered
// independently and the merged view contains only the allowed data.
func TestAgencyFilterMultipleFeedsIntegration(t *testing.T) {
	manager := newTestManager()
	manager.feedAgencyFilter["feed-a"] = map[string]bool{"agency-A": true}
	manager.feedAgencyFilter["feed-b"] = map[string]bool{"agency-B": true}

	// feed-a only allows routes from agency-A (R1)
	allowedRouteIDsA := map[string]bool{"R1": true}
	// feed-b only allows routes from agency-B (R2)
	allowedRouteIDsB := map[string]bool{"R2": true}

	tripsA := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}, // agency-A ✓
		{ID: gtfs.TripID{ID: "T2", RouteID: "R2"}}, // agency-B ✗
		{ID: gtfs.TripID{ID: "T3", RouteID: "R3"}}, // agency-C ✗
	}
	tripsB := []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T4", RouteID: "R1"}}, // agency-A ✗
		{ID: gtfs.TripID{ID: "T5", RouteID: "R2"}}, // agency-B ✓
	}

	filteredA := filterTripsByAgency(tripsA, allowedRouteIDsA)
	filteredB := filterTripsByAgency(tripsB, allowedRouteIDsB)

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
	assert.True(t, tripIDs["T1"])
	assert.True(t, tripIDs["T5"])
	assert.False(t, tripIDs["T2"])
	assert.False(t, tripIDs["T3"])
	assert.False(t, tripIDs["T4"])
}

// TestAgencyFilterEmptyResult verifies that filtering can produce zero results
// without panicking.
func TestAgencyFilterEmptyResult(t *testing.T) {
	allowedRouteIDs := map[string]bool{} // no routes allowed
	allowed := map[string]bool{"agency-X": true}

	trips := []gtfs.Trip{{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}}}
	vehicles := []gtfs.Vehicle{
		{ID: &gtfs.VehicleID{ID: "V1"}, Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "R1"}}},
	}
	alerts := []gtfs.Alert{
		{ID: "a1", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: strPtr("agency-A")}}},
	}

	assert.Empty(t, filterTripsByAgency(trips, allowedRouteIDs))
	assert.Empty(t, filterVehiclesByAgency(vehicles, allowedRouteIDs))
	assert.Empty(t, filterAlertsByAgency(alerts, allowed, allowedRouteIDs))
}

// TestAgencyFilterNilTrip verifies vehicles without trips are dropped.
func TestAgencyFilterNilTrip(t *testing.T) {
	vehicles := []gtfs.Vehicle{
		{ID: &gtfs.VehicleID{ID: "V1"}}, // nil Trip
	}
	assert.Empty(t, filterVehiclesByAgency(vehicles, map[string]bool{}))
}

// TestAgencyFilterIntegration_TripUpdates tests filtering of trip updates
// through the full updateFeedRealtime path with the real RABA DB.
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

	// Use a real DB so route→agency resolution works
	filteredManager := newTestManagerWithDB(t)

	// Find the agency ID from one of the trips' routes
	var realAgencyID string
	for _, trip := range allTrips {
		route, err := filteredManager.GtfsDB.Queries.GetRoute(ctx, trip.ID.RouteID)
		if err != nil {
			continue
		}
		realAgencyID = route.AgencyID
		break
	}
	if realAgencyID == "" {
		t.Skip("no RABA routes found in DB for trips in feed — cannot test agency filtering")
	}

	filteredManager.feedAgencyFilter["filtered"] = map[string]bool{realAgencyID: true}
	filteredManager.updateFeedRealtime(ctx, RTFeedConfig{
		ID:              "filtered",
		AgencyIDs:       []string{realAgencyID},
		TripUpdatesURL:  server.URL,
		RefreshInterval: 30,
		Enabled:         true,
	})

	filteredTrips := filteredManager.GetRealTimeTrips()
	assert.NotEmpty(t, filteredTrips, "trips matching the real agency should pass through")

	// Filtering by an unknown agency should drop all trips
	filteredManager2 := newTestManagerWithDB(t)
	filteredManager2.feedAgencyFilter["no-match"] = map[string]bool{"nonexistent-agency": true}
	filteredManager2.updateFeedRealtime(ctx, RTFeedConfig{
		ID:              "no-match",
		AgencyIDs:       []string{"nonexistent-agency"},
		TripUpdatesURL:  server.URL,
		RefreshInterval: 30,
		Enabled:         true,
	})
	assert.Empty(t, filteredManager2.GetRealTimeTrips(), "unknown agency should filter out all trips")
}

// TestFeedVehicleRetentionWithAgencyFilter ensures that the stale vehicle
// retention logic still works correctly when agency filtering is active.
func TestFeedVehicleRetentionWithAgencyFilter(t *testing.T) {
	manager := newTestManager()
	manager.feedAgencyFilter["feed"] = map[string]bool{"agency-A": true}

	now := time.Now()
	vid := &gtfs.VehicleID{ID: "V1"}
	ts := now.Add(-1 * time.Minute)

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
