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

// TestClearStaleTripUpdates guards #1960: trip updates must age out on their
// own sub-feed clock, because a partial success from a sibling sub-feed keeps
// resetting the feed-wide staleness timer, so clearFeedData never runs.
func TestClearStaleTripUpdates(t *testing.T) {
	stale := time.Now().Add(-staleFeedThreshold - time.Minute)

	tests := []struct {
		name          string
		lastSuccess   map[string]time.Time
		trips         map[string][]gtfs.Trip
		expectCleared bool
	}{
		{
			name:          "trip updates older than threshold are cleared",
			lastSuccess:   map[string]time.Time{"feed": stale},
			trips:         map[string][]gtfs.Trip{"feed": {{ID: gtfs.TripID{ID: "t1"}}}},
			expectCleared: true,
		},
		{
			name:          "trip updates newer than threshold are kept",
			lastSuccess:   map[string]time.Time{"feed": time.Now()},
			trips:         map[string][]gtfs.Trip{"feed": {{ID: gtfs.TripID{ID: "t1"}}}},
			expectCleared: false,
		},
		{
			name:          "feed that never succeeded a trip fetch has nothing to clear",
			trips:         map[string][]gtfs.Trip{},
			expectCleared: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{
				feedTrips:           test.trips,
				feedTripLastSuccess: test.lastSuccess,
			}
			manager.rebuildMergedRealtimeLocked()

			cleared := manager.clearStaleTripUpdates("feed")

			assert.Equal(t, test.expectCleared, cleared)
			if test.expectCleared {
				assert.Empty(t, manager.feedTrips["feed"])
				assert.Len(t, manager.GetRealTimeTrips(), 0)
			} else {
				assert.Equal(t, test.trips["feed"], manager.feedTrips["feed"])
			}
		})
	}
}

// TestStaleTripUpdatesClearedWhileVehiclesKeepSucceeding reproduces the #1960
// poller tick at update level: the trip-updates URL fails while the
// vehicle-positions URL succeeds, so the update reports partial success —
// exactly the case that resets the feed-wide timer on main. The per-tick
// clearStaleTripUpdates call (same order as pollFeed) must drop the aged trips
// and keep the fresh vehicles.
func TestStaleTripUpdatesClearedWhileVehiclesKeepSucceeding(t *testing.T) {
	manager := newTestManager()

	freshAt := time.Unix(1_700_000_000, 0).UTC()
	vehiclePayload := encodeVehicleFeed(freshAt, []*gtfsrt.VehiclePosition{{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh-1")},
		Timestamp: proto.Uint64(uint64(freshAt.Unix())),
	}})
	vehicleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(vehiclePayload)
	}))
	defer vehicleServer.Close()

	tripServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "trip updates unavailable", http.StatusInternalServerError)
	}))
	defer tripServer.Close()

	feed := RTFeedConfig{
		ID:                  "partial-feed",
		TripUpdatesURL:      tripServer.URL,
		VehiclePositionsURL: vehicleServer.URL,
	}

	// Seed as if trip updates last succeeded long ago, then the trip-updates
	// URL started failing.
	manager.realTimeMutex.Lock()
	manager.feedTrips[feed.ID] = []gtfs.Trip{{ID: gtfs.TripID{ID: "stale-trip"}}}
	manager.feedTripLastSuccess = map[string]time.Time{feed.ID: time.Now().Add(-staleFeedThreshold - time.Minute)}
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	// pollFeed order: update, then age out stale sub-feed data.
	hasNewData := manager.updateFeedRealtime(context.Background(), feed)
	require.True(t, hasNewData, "vehicle success must still count as partial success")
	require.True(t, manager.clearStaleTripUpdates(feed.ID), "stale trips must age out on their own clock")

	assert.Empty(t, manager.feedTrips[feed.ID], "stale trips must not survive a failing trip-updates URL")
	assert.NotEmpty(t, manager.GetRealTimeVehicles(), "fresh vehicles must survive")
}

// TestTripUpdatesSuccessStampsLastSuccess guards the other half of #1960: the
// feedTripLastSuccess stamp in updateFeedRealtime. Seed an old stamp, let the
// trip-updates URL succeed, and the stamp must be refreshed — otherwise
// clearStaleTripUpdates would age out healthy trips.
func TestTripUpdatesSuccessStampsLastSuccess(t *testing.T) {
	manager := newTestManager()

	freshAt := time.Unix(1_700_000_000, 0).UTC()
	tripServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(encodeTripFeed(freshAt, "trip-fresh", "route-1"))
	}))
	defer tripServer.Close()

	feed := RTFeedConfig{ID: "stamp-feed", TripUpdatesURL: tripServer.URL}

	// Seed as if trip updates had been failing long enough to go stale just
	// before this successful fetch.
	manager.realTimeMutex.Lock()
	manager.feedTripLastSuccess = map[string]time.Time{feed.ID: time.Now().Add(-staleFeedThreshold - time.Minute)}
	manager.realTimeMutex.Unlock()

	require.True(t, manager.updateFeedRealtime(context.Background(), feed), "the trip-updates fetch succeeds")

	// The successful fetch must have stamped the clock: the old seed must not
	// make the just-fetched trips look stale.
	require.False(t, manager.clearStaleTripUpdates(feed.ID), "a successful trip fetch must refresh feedTripLastSuccess")
	assert.NotEmpty(t, manager.feedTrips[feed.ID], "fresh trips must survive")
}
