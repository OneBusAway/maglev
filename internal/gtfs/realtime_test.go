package gtfs

import (
	"context"
	"fmt"
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
		feedTrips: map[string][]gtfs.Trip{
			"feed-0": {
				{
					ID: gtfs.TripID{ID: "trip1"},
				},
				{
					ID: gtfs.TripID{ID: "trip2"},
				},
			},
		},
	}

	manager.rebuildMergedRealtimeLocked()

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
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-0": {
				{
					Trip: trip1,
				},
				{
					Trip: trip2,
				},
			},
		},
	}

	manager.rebuildMergedRealtimeLocked()

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
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-0": {
				{
					ID: vehicleID1,
				},
				{
					ID: vehicleID2,
				},
			},
		},
	}

	manager.rebuildMergedRealtimeLocked()

	assert.NotNil(t, manager.realTimeVehicleLookupByVehicle)
	assert.Len(t, manager.realTimeVehicleLookupByVehicle, 2)
	assert.Equal(t, 0, manager.realTimeVehicleLookupByVehicle["vehicle1"])
	assert.Equal(t, 1, manager.realTimeVehicleLookupByVehicle["vehicle2"])
}

func TestRebuildRealTimeVehicleLookupByVehicle_WithInvalidIDs(t *testing.T) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-0": {
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
	}

	manager.rebuildMergedRealtimeLocked()

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

// TestStaleFeedRejected verifies that feeds with stale FeedHeader timestamps
// are rejected and vehicles from the newer feed are preserved. This tests
// the feed-level freshness guard that prevents out-of-order feed updates.
func TestStaleFeedRejected(t *testing.T) {
	// Create a test server that serves the same RABA vehicle data
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
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

	// Simulate a stale feed by manually setting the stored timestamp to a very
	// large value (future timestamp), so the next update will be rejected.
	manager.realTimeMutex.Lock()
	manager.feedVehicleTimestamp[feed.ID] = uint64(time.Now().Add(1 * time.Hour).UnixNano())
	manager.realTimeMutex.Unlock()

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

// ptr is a helper function to create a pointer to a time.Time value.
func ptr(t time.Time) *time.Time {
	return &t
}
