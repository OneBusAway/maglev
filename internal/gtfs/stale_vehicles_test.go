package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestClearStaleVehicles guards #2210: vehicles must age out on their own
// sub-feed clock, because a partial success from a sibling sub-feed keeps
// resetting the feed-wide staleness timer, so clearFeedData never runs.
func TestClearStaleVehicles(t *testing.T) {
	stale := time.Now().Add(-staleFeedThreshold - time.Minute)
	busID := "bus-1"
	bus := gtfs.Vehicle{ID: &gtfs.VehicleID{ID: busID}}

	tests := []struct {
		name          string
		lastSuccess   map[string]time.Time
		vehicles      map[string][]gtfs.Vehicle
		expectCleared bool
	}{
		{
			name:          "vehicles older than threshold are cleared",
			lastSuccess:   map[string]time.Time{"feed": stale},
			vehicles:      map[string][]gtfs.Vehicle{"feed": {bus}},
			expectCleared: true,
		},
		{
			name:          "vehicles newer than threshold are kept",
			lastSuccess:   map[string]time.Time{"feed": time.Now()},
			vehicles:      map[string][]gtfs.Vehicle{"feed": {bus}},
			expectCleared: false,
		},
		{
			name:          "feed that never stored vehicles has nothing to clear",
			vehicles:      map[string][]gtfs.Vehicle{},
			expectCleared: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{
				feedVehicles:           test.vehicles,
				feedVehicleLastSuccess: test.lastSuccess,
			}
			manager.rebuildMergedRealtimeLocked()

			cleared := manager.clearStaleVehicles("feed")

			assert.Equal(t, test.expectCleared, cleared)
			if test.expectCleared {
				assert.Empty(t, manager.feedVehicles["feed"])
				assert.Empty(t, manager.GetRealTimeVehicles())
				assert.Empty(t, manager.GetLiveRealTimeVehicles())
			} else {
				assert.Equal(t, test.vehicles["feed"], manager.feedVehicles["feed"])
			}
		})
	}
}

// TestStaleVehiclesClearedWhileTripUpdatesKeepSucceeding reproduces #2210 at
// update level: the vehicle-positions sub-feed stops storing payloads while
// the trip-updates URL succeeds, so the update reports partial success, which
// resets the feed-wide timer. The per-tick clearStaleVehicles call (same order
// as pollFeed) must drop the aged vehicles and keep the fresh trips. A payload
// is not stored either when the URL fails or when its header is older than
// the last stored one.
func TestStaleVehiclesClearedWhileTripUpdatesKeepSucceeding(t *testing.T) {
	freshAt := time.Unix(1_700_000_000, 0).UTC()

	tests := []struct {
		name          string
		vehicleServer http.HandlerFunc
	}{
		{
			name: "vehicle-positions URL fails",
			vehicleServer: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "vehicle positions unavailable", http.StatusInternalServerError)
			},
		},
		{
			name: "vehicle-positions header is older than the stored one",
			vehicleServer: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-protobuf")
				_, _ = w.Write(encodeVehicleFeed(freshAt.Add(-time.Hour), []*gtfsrt.VehiclePosition{{
					Vehicle: &gtfsrt.VehicleDescriptor{Id: proto.String("bus-old")},
				}}))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager()

			vehicleServer := httptest.NewServer(test.vehicleServer)
			defer vehicleServer.Close()

			tripServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-protobuf")
				_, _ = w.Write(encodeTripFeed(freshAt, "trip-fresh", "route-1"))
			}))
			defer tripServer.Close()

			feed := RTFeedConfig{
				ID:                  "partial-feed",
				TripUpdatesURL:      tripServer.URL,
				VehiclePositionsURL: vehicleServer.URL,
			}

			// Seed as if a vehicle payload was last stored long ago, with a
			// header newer than anything the vehicle server sends now.
			manager.realTimeMutex.Lock()
			manager.feedVehicles[feed.ID] = []gtfs.Vehicle{{ID: &gtfs.VehicleID{ID: "bus-1"}}}
			manager.feedVehicleTimestamp[feed.ID] = uint64(freshAt.UnixNano())
			manager.feedVehicleLastSuccess = map[string]time.Time{feed.ID: time.Now().Add(-staleFeedThreshold - time.Minute)}
			manager.rebuildMergedRealtimeLocked()
			manager.realTimeMutex.Unlock()

			// pollFeed order: update, then age out stale sub-feed data.
			hasNewData := manager.updateFeedRealtime(context.Background(), feed)
			require.True(t, hasNewData, "trip success must still count as partial success")
			require.True(t, manager.clearStaleVehicles(feed.ID), "stale vehicles must age out on their own clock")

			assert.Empty(t, manager.GetRealTimeVehicles(), "stale vehicles must not survive")
			assert.Empty(t, manager.GetLiveRealTimeVehicles(), "stale vehicles must not be reported as live")
			assert.NotEmpty(t, manager.GetRealTimeTrips(), "fresh trips must survive")
		})
	}
}

// TestVehiclePositionsSuccessStampsLastSuccess guards the other half of
// #2210: the feedVehicleLastSuccess stamp in updateFeedRealtime. Seed an old
// stamp, let the vehicle-positions URL store a payload, and the stamp must be
// refreshed, otherwise clearStaleVehicles would age out healthy vehicles.
func TestVehiclePositionsSuccessStampsLastSuccess(t *testing.T) {
	manager := newTestManager()

	freshAt := time.Unix(1_700_000_000, 0).UTC()
	vehicleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(encodeVehicleFeed(freshAt, []*gtfsrt.VehiclePosition{{
			Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("bus-fresh")},
			Timestamp: proto.Uint64(uint64(freshAt.Unix())),
		}}))
	}))
	defer vehicleServer.Close()

	feed := RTFeedConfig{ID: "stamp-feed", VehiclePositionsURL: vehicleServer.URL}

	// Seed as if vehicle positions had been failing long enough to go stale
	// just before this successful fetch.
	manager.realTimeMutex.Lock()
	manager.feedVehicleLastSuccess = map[string]time.Time{feed.ID: time.Now().Add(-staleFeedThreshold - time.Minute)}
	manager.realTimeMutex.Unlock()

	require.True(t, manager.updateFeedRealtime(context.Background(), feed), "the vehicle-positions fetch succeeds")

	// The stored payload must have stamped the clock: the old seed must not
	// make the just-fetched vehicles look stale.
	require.False(t, manager.clearStaleVehicles(feed.ID), "a stored vehicle payload must refresh feedVehicleLastSuccess")
	assert.NotEmpty(t, manager.GetRealTimeVehicles(), "fresh vehicles must survive")
}
