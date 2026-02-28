package gtfs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
