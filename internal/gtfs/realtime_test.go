package gtfs

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	logging "maglev.onebusaway.org/internal/logging"
)

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
		realTimeMutex: sync.RWMutex{},
		feedData: map[string]*FeedData{
			"feed-0": {
				Trips: []gtfs.Trip{
					{
						ID: gtfs.TripID{ID: "trip1"},
					},
					{
						ID: gtfs.TripID{ID: "trip2"},
					},
				},
			},
		},
	}

	manager.buildMergedRealtime()

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
		realTimeMutex: sync.RWMutex{},
		feedData: map[string]*FeedData{
			"feed-0": {
				Vehicles: []gtfs.Vehicle{
					{
						Trip: trip1,
					},
					{
						Trip: trip2,
					},
				},
			},
		},
	}

	manager.buildMergedRealtime()

	assert.NotNil(t, manager.realTimeVehicleLookupByTrip)
	assert.Len(t, manager.realTimeVehicleLookupByTrip, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByTrip["trip1"])
	assert.Equal(t, 1, manager.realTimeVehicleLookupByTrip["trip2"])
}

func TestRebuildRealTimeVehicleLookupByVehicle(t *testing.T) {
	vehicleID1 := &gtfs.VehicleID{ID: "vehicle1"}
	vehicleID2 := &gtfs.VehicleID{ID: "vehicle2"}

	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData: map[string]*FeedData{
			"feed-0": {
				Vehicles: []gtfs.Vehicle{
					{
						ID: vehicleID1,
					},
					{
						ID: vehicleID2,
					},
				},
			},
		},
	}

	manager.buildMergedRealtime()

	assert.NotNil(t, manager.realTimeVehicleLookupByVehicle)
	assert.Len(t, manager.realTimeVehicleLookupByVehicle, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByVehicle["vehicle1"])
	assert.Equal(t, 1, manager.realTimeVehicleLookupByVehicle["vehicle2"])
}

func TestRebuildRealTimeVehicleLookupByVehicle_WithInvalidIDs(t *testing.T) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData: map[string]*FeedData{
			"feed-0": {
				Vehicles: []gtfs.Vehicle{
					{
						ID: &gtfs.VehicleID{ID: "vehicle1"},
					},
					{
						ID: nil,
					},
					{
						ID: &gtfs.VehicleID{ID: ""},
					},
					{
						ID: &gtfs.VehicleID{ID: "vehicle3"},
					},
				},
			},
		},
	}

	manager.buildMergedRealtime()

	assert.NotNil(t, manager.realTimeVehicleLookupByVehicle)
	assert.Len(t, manager.realTimeVehicleLookupByVehicle, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByVehicle["vehicle1"])
	assert.Equal(t, 3, manager.realTimeVehicleLookupByVehicle["vehicle3"])
}

func TestLoadRealtimeData_Non200StatusCode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"InternalServerError", http.StatusInternalServerError},
		{"NotFound", http.StatusNotFound},
		{"Forbidden", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			result, err := loadRealtimeData(context.Background(), server.URL, nil)
			assert.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), fmt.Sprintf("%d", tt.statusCode))
		})
	}
}

func TestEnabledFeeds(t *testing.T) {
	tests := []struct {
		name    string
		feeds   []RTFeedConfig
		wantIDs []string
	}{
		{
			name:    "empty config returns no feeds",
			feeds:   nil,
			wantIDs: nil,
		},
		{
			name: "disabled feed is excluded",
			feeds: []RTFeedConfig{
				{ID: "disabled", VehiclePositionsURL: "http://example.com/vp", Enabled: false},
			},
			wantIDs: nil,
		},
		{
			name: "enabled feed with no URLs is excluded",
			feeds: []RTFeedConfig{
				{ID: "no-urls", Enabled: true},
			},
			wantIDs: nil,
		},
		{
			name: "enabled feed with trip-updates URL is included",
			feeds: []RTFeedConfig{
				{ID: "trip-feed", TripUpdatesURL: "http://example.com/tu", Enabled: true},
			},
			wantIDs: []string{"trip-feed"},
		},
		{
			name: "enabled feed with vehicle-positions URL is included",
			feeds: []RTFeedConfig{
				{ID: "vp-feed", VehiclePositionsURL: "http://example.com/vp", Enabled: true},
			},
			wantIDs: []string{"vp-feed"},
		},
		{
			name: "enabled feed with service-alerts URL is included",
			feeds: []RTFeedConfig{
				{ID: "alert-feed", ServiceAlertsURL: "http://example.com/sa", Enabled: true},
			},
			wantIDs: []string{"alert-feed"},
		},
		{
			name: "mixed enabled and disabled feeds",
			feeds: []RTFeedConfig{
				{ID: "active", VehiclePositionsURL: "http://example.com/vp", Enabled: true},
				{ID: "inactive", VehiclePositionsURL: "http://example.com/vp", Enabled: false},
				{ID: "no-url", Enabled: true},
			},
			wantIDs: []string{"active"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{RTFeeds: tt.feeds}
			got := cfg.enabledFeeds()

			if tt.wantIDs == nil {
				assert.Empty(t, got)
				return
			}

			gotIDs := make([]string, len(got))
			for i, f := range got {
				gotIDs[i] = f.ID
			}
			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

func TestClearFeedData(t *testing.T) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData: map[string]*FeedData{
			"test_feed": {
				Trips:           []gtfs.Trip{{ID: gtfs.TripID{ID: "trip1"}}},
				Vehicles:        []gtfs.Vehicle{{ID: &gtfs.VehicleID{ID: "veh1"}}},
				Alerts:          []gtfs.Alert{{ID: "alert1"}},
				VehicleLastSeen: make(map[string]time.Time),
			},
		},
		feedLastUpdate: map[string]time.Time{
			"test_feed": time.Now(),
		},
	}

	// Warm up realTime lookup array cache
	manager.buildMergedRealtime()
	assert.Len(t, manager.GetRealTimeTrips(), 1, "Should have 1 trip initially")
	assert.Contains(t, manager.feedLastUpdate, "test_feed", "feedLastUpdate should exist initially")

	// Trigger the clearing mechanism
	manager.clearFeedData("test_feed")

	feed := manager.feedData["test_feed"]
	assert.Empty(t, feed.Trips, "feedTrips should be empty after clearing")
	assert.Empty(t, feed.Vehicles, "feedVehicles should be empty after clearing")
	assert.Empty(t, feed.Alerts, "feedAlerts should be empty after clearing")
	assert.NotContains(t, manager.feedLastUpdate, "test_feed", "feedLastUpdate should be removed after clearing")
	assert.Len(t, manager.GetRealTimeTrips(), 0, "Global trip lookup should be empty")
	assert.Len(t, manager.GetRealTimeVehicles(), 0, "Global vehicle lookup should be empty")
}

// A minimal valid GTFS-RT protobuf payload containing just a GTFS Realtime version header.
// Parsed successfully as an empty feed.
var emptyValidGTFSRTPayload = []byte{0x0a, 0x05, 0x0a, 0x03, 0x32, 0x2e, 0x30}

func TestUpdateFeedRealtime_RetainsOldDataOnError(t *testing.T) {
	// Setup a server that always returns 500 (causes error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	manager := &Manager{
		feedData:      make(map[string]*FeedData),
		realTimeMutex: sync.RWMutex{},
	}

	oldTrips := []gtfs.Trip{{ID: gtfs.TripID{ID: "old-trip"}}}
	manager.feedData["feed1"] = &FeedData{
		Trips:           oldTrips,
		VehicleLastSeen: make(map[string]time.Time),
	}

	feedCfg := RTFeedConfig{
		ID:             "feed1",
		TripUpdatesURL: server.URL,
	}

	manager.updateFeedRealtime(context.Background(), feedCfg)

	feed := manager.feedData["feed1"]
	assert.Len(t, feed.Trips, 1)
	assert.Equal(t, "old-trip", feed.Trips[0].ID.ID, "Old data should be retained on error")
}

func TestUpdateFeedRealtime_SuccessReplacesOld(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(emptyValidGTFSRTPayload)
	}))
	defer server.Close()

	manager := &Manager{
		feedData:      make(map[string]*FeedData),
		realTimeMutex: sync.RWMutex{},
	}

	oldTrips := []gtfs.Trip{{ID: gtfs.TripID{ID: "old-trip"}}}
	manager.feedData["feed1"] = &FeedData{
		Trips:           oldTrips,
		VehicleLastSeen: make(map[string]time.Time),
	}

	feedCfg := RTFeedConfig{
		ID:             "feed1",
		TripUpdatesURL: server.URL, // Succeeds with empty payload
	}

	manager.updateFeedRealtime(context.Background(), feedCfg)

	feed := manager.feedData["feed1"]
	assert.Len(t, feed.Trips, 0, "Old data should be replaced by the empty feed on success")
}

func TestUpdateFeedRealtime_ReturnsFalseOnFailure(t *testing.T) {
	// Setup a server that always returns 500 error simulating an outage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData:      make(map[string]*FeedData),
	}

	cfg := RTFeedConfig{
		ID:                  "fail-feed",
		TripUpdatesURL:      server.URL,
		VehiclePositionsURL: server.URL,
		ServiceAlertsURL:    server.URL,
	}

	hasNewData := manager.updateFeedRealtime(context.Background(), cfg)

	assert.False(t, hasNewData, "Should return false when all fetches fail")
}

// TestStaleFeedRejected verifies that feeds with stale FeedHeader timestamps
// are rejected and vehicles from the newer feed are preserved. This tests
// the feed-level freshness guard that prevents out-of-order feed updates.
func TestStaleFeedRejected(t *testing.T) {
	// Read the test data before creating the server to ensure proper error handling
	data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
	require.NoError(t, err, "failed to read RABA vehicle positions test data")

	// Create a test server that serves the same RABA vehicle data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	manager := newTestManager()
	ctx := context.Background()

	feed := RTFeedConfig{
		ID:                  "freshness-test",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	}

	// First poll: load vehicles with a FeedHeader timestamp
	manager.updateFeedRealtime(ctx, feed)
	firstPoll := manager.GetRealTimeVehicles()
	require.NotEmpty(t, firstPoll, "first poll should load vehicles")
	firstCount := len(firstPoll)

	// Verify the feed has a FeedHeader timestamp — this test
	// exercises the freshness guard, which only applies to feeds
	// with a non-zero CreatedAt.
	manager.feedData[feed.ID].mu.RLock()
	require.NotZero(t, manager.feedData[feed.ID].VehicleTimestamp,
		"RABA feed must have FeedHeader timestamp for this test to be meaningful")
	manager.feedData[feed.ID].mu.RUnlock()

	// Simulate a stale feed by manually setting the stored timestamp to a very
	// large value (future timestamp), so the next update will be rejected.
	manager.feedData[feed.ID].mu.Lock()
	manager.feedData[feed.ID].VehicleTimestamp = uint64(time.Now().Add(1 * time.Hour).UnixNano())
	manager.feedData[feed.ID].mu.Unlock()

	// Second poll: attempt to update with same feed URL (same data, same timestamp)
	// This should be rejected because the stored timestamp is in the future
	manager.updateFeedRealtime(ctx, feed)

	// Verify vehicles from first poll are preserved (not overwritten)
	secondPoll := manager.GetRealTimeVehicles()
	assert.Len(t, secondPoll, firstCount, "stale feed should be rejected, preserving first poll vehicles")

	// Extract vehicle IDs from both polls
	firstIDs := make(map[string]bool)
	for _, v := range firstPoll {
		if v.ID != nil {
			firstIDs[v.ID.ID] = true
		}
	}

	// Verify all vehicles from second poll came from first poll
	for _, v := range secondPoll {
		if v.ID != nil {
			assert.True(t, firstIDs[v.ID.ID], "vehicle should come from first poll, not stale feed")
		}
	}
}

// TestVehicleMerge_StaleIgnored ensures that when a feed update contains a
// vehicle entity whose timestamp is older than the one already stored in the
// manager, the older update is ignored and the existing (newer) record is
// preserved. The feed itself is kept "fresh" so the update is applied at the
// feed level.
func TestVehicleMerge_StaleIgnored(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	// capture logs for verification
	var buf bytes.Buffer
	logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)
	ctx = logging.WithLogger(ctx, logger)

	// create a server whose response can be modified between polls
	var mu sync.Mutex
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	feed := RTFeedConfig{ID: "test-feed", VehiclePositionsURL: server.URL, RefreshInterval: 30, Enabled: true}

	// first poll: introduce a vehicle with a recent timestamp
	t1 := time.Now()
	vehicle := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh1")},
		Timestamp: proto.Uint64(uint64(t1.Unix())),
	}
	mu.Lock()
	payload = encodeVehicleFeed(t1, []*gtfsrt.VehiclePosition{vehicle})
	mu.Unlock()
	manager.updateFeedRealtime(ctx, feed)
	first := manager.GetRealTimeVehicles()
	require.Len(t, first, 1)
	existing := first[0]
	require.NotNil(t, existing.Timestamp)
	existingTime := *existing.Timestamp

	// second poll: same feed header newer, but vehicle timestamp older
	t2 := t1.Add(time.Second)
	stale := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh1")},
		Timestamp: proto.Uint64(uint64(t1.Add(-time.Minute).Unix())),
	}
	mu.Lock()
	payload = encodeVehicleFeed(t2, []*gtfsrt.VehiclePosition{stale})
	mu.Unlock()
	manager.updateFeedRealtime(ctx, feed)

	second := manager.GetRealTimeVehicles()
	require.Len(t, second, 1)
	if second[0].Timestamp == nil {
		t.Fatalf("expected existing timestamp to be preserved, got nil")
	}
	assert.Equal(t, existingTime, *second[0].Timestamp, "stale incoming update should be ignored")

	// log should mention a stale vehicle entity being skipped
	logOutput := buf.String()
	assert.Contains(t, logOutput, "skipping_stale_vehicle_entity")
}

// TestVehicleMerge_MixedFreshAndStale sends a feed that contains both a newer
// and an older vehicle update relative to the manager's existing state. The
// fresh entity should update while the stale one should be preserved.
func TestVehicleMerge_MixedFreshAndStale(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	var mu sync.Mutex
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	feed := RTFeedConfig{ID: "mixed-feed", VehiclePositionsURL: server.URL, RefreshInterval: 30, Enabled: true}

	// initial state: only vehicle A at time tA
	tA := time.Now()
	vehA := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("A")},
		Timestamp: proto.Uint64(uint64(tA.Unix())),
	}
	mu.Lock()
	payload = encodeVehicleFeed(tA, []*gtfsrt.VehiclePosition{vehA})
	mu.Unlock()
	manager.updateFeedRealtime(ctx, feed)

	// second update: A arrives stale, B arrives fresh
	tBheader := tA.Add(time.Second)
	staleA := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("A")},
		Timestamp: proto.Uint64(uint64(tA.Add(-time.Minute).Unix())),
	}
	freshB := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("B")},
		Timestamp: proto.Uint64(uint64(tA.Add(time.Minute).Unix())),
	}
	mu.Lock()
	payload = encodeVehicleFeed(tBheader, []*gtfsrt.VehiclePosition{staleA, freshB})
	mu.Unlock()
	manager.updateFeedRealtime(ctx, feed)

	vehicles := manager.GetRealTimeVehicles()
	assert.Len(t, vehicles, 2)
	var foundA, foundB bool
	for _, v := range vehicles {
		if v.ID != nil && v.ID.ID == "A" {
			foundA = true
			assert.Equal(t, tA.Unix(), v.Timestamp.Unix(), "A should retain original timestamp")
		}
		if v.ID != nil && v.ID.ID == "B" {
			foundB = true
			assert.Equal(t, tA.Add(time.Minute).Unix(), v.Timestamp.Unix(), "B should be updated with fresh timestamp")
		}
	}
	assert.True(t, foundA && foundB, "both vehicles should be present")
}

// TestVehicleMerge_MissingTimestamp ensures that an incoming update with a
// nil timestamp does not crash and is treated as non-stale. In other words,
// the updated record (with nil timestamp) replaces the previous one.
func TestVehicleMerge_MissingTimestamp(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	var mu sync.Mutex
	var payload []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	feed := RTFeedConfig{ID: "nil-ts-feed", VehiclePositionsURL: server.URL, RefreshInterval: 30, Enabled: true}

	// initial poll with a timestamped vehicle
	t0 := time.Now()
	veh := &gtfsrt.VehiclePosition{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("nilveh")},
		Timestamp: proto.Uint64(uint64(t0.Unix())),
	}
	mu.Lock()
	payload = encodeVehicleFeed(t0, []*gtfsrt.VehiclePosition{veh})
	mu.Unlock()
	manager.updateFeedRealtime(ctx, feed)

	// second poll: same vehicle but timestamp field omitted
	t1 := t0.Add(time.Second)
	nilVeh := &gtfsrt.VehiclePosition{
		Vehicle: &gtfsrt.VehicleDescriptor{Id: proto.String("nilveh")},
		// Timestamp left nil
	}
	mu.Lock()
	payload = encodeVehicleFeed(t1, []*gtfsrt.VehiclePosition{nilVeh})
	mu.Unlock()
	manager.updateFeedRealtime(ctx, feed)

	vehicles := manager.GetRealTimeVehicles()
	require.Len(t, vehicles, 1)
	assert.Nil(t, vehicles[0].Timestamp, "incoming nil timestamp should replace existing")
}

// TestIsVehicleStale verifies the isVehicleStale function correctly compares
// vehicle timestamps to determine staleness.
func TestIsVehicleStale(t *testing.T) {
	tests := []struct {
		name     string
		existing gtfs.Vehicle
		incoming gtfs.Vehicle
		want     bool
	}{
		{
			name: "both timestamps present, incoming older",
			existing: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(1000, 0)),
			},
			incoming: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(900, 0)),
			},
			want: true, // incoming is older, so it's stale
		},
		{
			name: "both timestamps present, incoming newer",
			existing: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(900, 0)),
			},
			incoming: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(1000, 0)),
			},
			want: false, // incoming is newer, not stale
		},
		{
			name: "both timestamps present, equal",
			existing: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(1000, 0)),
			},
			incoming: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(1000, 0)),
			},
			want: false, // equal timestamps are not considered stale
		},
		{
			name: "existing timestamp nil",
			existing: gtfs.Vehicle{
				Timestamp: nil,
			},
			incoming: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(1000, 0)),
			},
			want: false, // cannot compare when existing is nil
		},
		{
			name: "incoming timestamp nil",
			existing: gtfs.Vehicle{
				Timestamp: ptr(time.Unix(1000, 0)),
			},
			incoming: gtfs.Vehicle{
				Timestamp: nil,
			},
			want: false, // cannot compare when incoming is nil
		},
		{
			name: "both timestamps nil",
			existing: gtfs.Vehicle{
				Timestamp: nil,
			},
			incoming: gtfs.Vehicle{
				Timestamp: nil,
			},
			want: false, // cannot compare when both are nil
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVehicleStale(tt.existing, tt.incoming)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestGetAlertsByIDs_RouteScoping verifies that route-level alert matching
// only fires for entities that have routeId with no stopId restriction.
// Entities with {routeId + stopId} are stop-specific and must NOT bleed into route level alerts.
func TestGetAlertsByIDs_RouteScoping(t *testing.T) {
	routeID := "route123"
	otherRoute := "other"
	stopID := "stop456"
	agencyID := "agency40"

	tests := []struct {
		name        string
		entities    []gtfs.AlertInformedEntity
		expectMatch bool
	}{
		{
			name:        "route-only entity matches",
			entities:    []gtfs.AlertInformedEntity{{RouteID: &routeID}},
			expectMatch: true,
		},
		{
			name:        "route+agency entity (no stop) matches",
			entities:    []gtfs.AlertInformedEntity{{RouteID: &routeID, AgencyID: &agencyID}},
			expectMatch: true,
		},
		{
			name:        "route+stop entity does not match route query",
			entities:    []gtfs.AlertInformedEntity{{RouteID: &routeID, StopID: &stopID}},
			expectMatch: false,
		},
		{
			name:        "route+agency+stop entity does not match route query",
			entities:    []gtfs.AlertInformedEntity{{RouteID: &routeID, AgencyID: &agencyID, StopID: &stopID}},
			expectMatch: false,
		},
		{
			name: "mixed entities: route+stop and route-only — matches via route-only",
			entities: []gtfs.AlertInformedEntity{
				{RouteID: &routeID, StopID: &stopID},
				{RouteID: &routeID},
			},
			expectMatch: true,
		},
		{
			name:        "different route does not match",
			entities:    []gtfs.AlertInformedEntity{{RouteID: &otherRoute}},
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &Manager{
				realTimeMutex:  sync.RWMutex{},
				realTimeAlerts: []gtfs.Alert{{ID: "alert1", InformedEntities: tt.entities}},
			}
			alerts := manager.GetAlertsByIDs("", routeID, "")
			if tt.expectMatch {
				assert.Len(t, alerts, 1)
			} else {
				assert.Empty(t, alerts)
			}
		})
	}
}

// TestGetAlertsByIDs_AgencyScoping verifies that agency-wide matching only fires
// for entities that have agencyId with no route or trip restriction.
func TestGetAlertsByIDs_AgencyScoping(t *testing.T) {
	agencyID := "agency40"
	routeID := "route123"
	tripID := gtfs.TripID{ID: "trip456"}

	tests := []struct {
		name        string
		entities    []gtfs.AlertInformedEntity
		expectMatch bool
	}{
		{
			name:        "agency-only entity matches",
			entities:    []gtfs.AlertInformedEntity{{AgencyID: &agencyID}},
			expectMatch: true,
		},
		{
			name:        "agency+route entity does not match agency-only query",
			entities:    []gtfs.AlertInformedEntity{{AgencyID: &agencyID, RouteID: &routeID}},
			expectMatch: false,
		},
		{
			name:        "agency+trip entity does not match agency-only query",
			entities:    []gtfs.AlertInformedEntity{{AgencyID: &agencyID, TripID: &tripID}},
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &Manager{
				realTimeMutex:  sync.RWMutex{},
				realTimeAlerts: []gtfs.Alert{{ID: "alert1", InformedEntities: tt.entities}},
			}
			alerts := manager.GetAlertsByIDs("", "", agencyID)
			if tt.expectMatch {
				assert.Len(t, alerts, 1)
			} else {
				assert.Empty(t, alerts)
			}
		})
	}
}

// encodeVehicleFeed constructs a GTFS-RT protobuf payload containing
// the provided vehicle positions. The header's timestamp is set to the given
// createdAt time (in seconds). This helper is used by multiple tests to simulate
// feeds with controllable timestamps.
func encodeVehicleFeed(createdAt time.Time, positions []*gtfsrt.VehiclePosition) []byte {
	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String("2.0"),
			Timestamp:           proto.Uint64(uint64(createdAt.Unix())),
		},
	}
	for i, vp := range positions {
		feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
			Id:      proto.String(fmt.Sprintf("e%d", i)),
			Vehicle: vp,
		})
	}
	b, err := proto.Marshal(feed)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal realtime feed: %s", err))
	}
	return b
}

// ptr is a helper function to create a pointer to a time.Time value.
func ptr(t time.Time) *time.Time {
	return &t
}

func TestCalculateBackoff(t *testing.T) {
	baseInterval := 30 * time.Second
	maxInterval := 5 * time.Minute

	tests := []struct {
		name              string
		consecutiveErrors int
		expectedBase      time.Duration
	}{
		{"1 error (2x)", 1, 60 * time.Second},
		{"2 errors (4x)", 2, 120 * time.Second},
		{"3 errors (8x)", 3, 240 * time.Second},
		{"4 errors (16x, capped at max)", 4, 300 * time.Second}, // 480s capped to 300s
		{"10 errors (capped at max)", 10, 300 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run a few times to account for jitter and ensure it stays in bounds
			for i := 0; i < 50; i++ {
				result := calculateBackoff(baseInterval, tt.consecutiveErrors, maxInterval)

				// Calculate acceptable jitter bounds (+/- 10%)
				minExpected := time.Duration(float64(tt.expectedBase) * 0.9)
				maxExpected := time.Duration(float64(tt.expectedBase) * 1.1)

				// Use GreaterOrEqual and LessOrEqual to satisfy testifylint
				assert.GreaterOrEqual(t, result, minExpected, "Result %v below minimum bounds %v", result, minExpected)
				assert.LessOrEqual(t, result, maxExpected, "Result %v above maximum bounds %v", result, maxExpected)
			}
		})
	}
}

func TestUpdateFeedRealtime_SubFeedSuccess_OrLogic(t *testing.T) {
	// A server that returns 200 OK AND a valid GTFS-RT protobuf payload
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		// Send a minimal valid GTFS-RT feed (just the header, no entities)
		payload := encodeVehicleFeed(time.Now(), nil)
		_, _ = w.Write(payload)
	}))
	defer goodServer.Close()

	// A server that returns 500 Error
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badServer.Close()

	// Fully initialize all maps to prevent "assignment to entry in nil map" panics
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedData:      make(map[string]*FeedData),
	}

	// 1. Test partial success (OR logic): Trip updates succeed, Vehicle positions fail
	cfg := RTFeedConfig{
		ID:                  "partial-fail-feed",
		TripUpdatesURL:      goodServer.URL, // Succeeds
		VehiclePositionsURL: badServer.URL,  // Fails
	}

	hasNewData := manager.updateFeedRealtime(context.Background(), cfg)
	assert.True(t, hasNewData, "OR check should return true if ANY configured sub-feed succeeds")

	// 2. Test full failure: Both fail
	cfgFail := RTFeedConfig{
		ID:                  "fail-feed",
		TripUpdatesURL:      badServer.URL,
		VehiclePositionsURL: badServer.URL,
	}

	hasNewDataFail := manager.updateFeedRealtime(context.Background(), cfgFail)
	assert.False(t, hasNewDataFail, "OR check should return false when ALL sub-feeds fail")

	// 3. Test full success: Both succeed
	cfgSuccess := RTFeedConfig{
		ID:                  "success-feed",
		TripUpdatesURL:      goodServer.URL,
		VehiclePositionsURL: goodServer.URL,
	}

	hasNewDataSuccess := manager.updateFeedRealtime(context.Background(), cfgSuccess)
	assert.True(t, hasNewDataSuccess, "OR check should return true when ALL sub-feeds succeed")
}

func TestUpdateFeedRealtime_StaleVehicles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(emptyValidGTFSRTPayload) // Empty -> no new vehicles fetched
	}))
	defer server.Close()

	manager := &Manager{
		feedData:      make(map[string]*FeedData),
		realTimeMutex: sync.RWMutex{},
	}

	now := time.Now()

	// v1 seen 5 mins ago (should be kept)
	v1 := gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "v1"}}

	// v2 seen 20 mins ago (should be removed, staleVehicleTimeout = 15m)
	v2 := gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "v2"}}

	manager.feedData["feed1"] = &FeedData{
		Vehicles: []gtfs.Vehicle{v1, v2},
		VehicleLastSeen: map[string]time.Time{
			"v1": now.Add(-5 * time.Minute),
			"v2": now.Add(-20 * time.Minute),
		},
	}

	feedCfg := RTFeedConfig{
		ID:                  "feed1",
		VehiclePositionsURL: server.URL,
	}

	manager.updateFeedRealtime(context.Background(), feedCfg)

	feed := manager.feedData["feed1"]

	assert.Len(t, feed.Vehicles, 1, "Only 1 vehicle should remain")
	if len(feed.Vehicles) > 0 {
		assert.Equal(t, "v1", feed.Vehicles[0].ID.ID, "Recently seen vehicle should be kept")
	}

	_, ok1 := feed.VehicleLastSeen["v1"]
	assert.True(t, ok1, "v1 should still be in VehicleLastSeen map")

	_, ok2 := feed.VehicleLastSeen["v2"]
	assert.False(t, ok2, "v2 should be removed from VehicleLastSeen map due to staleness")
}
