package gtfs

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/logging"
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

// TestFrozenTripUpdatesAgeOutWhileVehiclesKeepSucceeding guards #2022: when a
// trip-updates feed gets stuck and returns HTTP 200 with an unchanged header
// timestamp while sibling sub-feeds (like vehicle positions) keep succeeding,
// the frozen trip payload must be skipped as stale and not update feedTripLastSuccess.
// clearStaleTripUpdates must age out and clear the frozen trip data after
// staleFeedThreshold, while fresh vehicles survive.
func TestFrozenTripUpdatesAgeOutWhileVehiclesKeepSucceeding(t *testing.T) {
	manager := newTestManager()

	freshAt := time.Unix(1_700_000_000, 0).UTC()
	var vehicleMu sync.Mutex
	vehiclePayload := encodeVehicleFeed(freshAt, []*gtfsrt.VehiclePosition{{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh-1")},
		Timestamp: proto.Uint64(uint64(freshAt.Unix())),
	}})
	vehicleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vehicleMu.Lock()
		defer vehicleMu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(vehiclePayload)
	}))
	defer vehicleServer.Close()

	// Frozen trip updates server: always returns the same createdAt timestamp
	frozenTripPayload := encodeTripFeed(freshAt, "frozen-trip", "route-1")
	tripServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(frozenTripPayload)
	}))
	defer tripServer.Close()

	feed := RTFeedConfig{
		ID:                  "frozen-feed",
		TripUpdatesURL:      tripServer.URL,
		VehiclePositionsURL: vehicleServer.URL,
	}

	// First poll: trip feed is accepted because prev watermark is 0.
	require.True(t, manager.updateFeedRealtime(context.Background(), feed))
	require.NotEmpty(t, manager.feedTrips[feed.ID], "initial trip payload must be stored")

	// Simulate passage of time past staleFeedThreshold without moving the trip watermark.
	manager.realTimeMutex.Lock()
	manager.feedTripLastSuccess[feed.ID] = time.Now().Add(-staleFeedThreshold - time.Minute)
	manager.realTimeMutex.Unlock()

	// Update vehicle payload so vehicle positions advances
	newerVehicleAt := freshAt.Add(time.Minute)
	vehicleMu.Lock()
	vehiclePayload = encodeVehicleFeed(newerVehicleAt, []*gtfsrt.VehiclePosition{{
		Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("veh-2")},
		Timestamp: proto.Uint64(uint64(newerVehicleAt.Unix())),
	}})
	vehicleMu.Unlock()

	// Subsequent poll: vehicle has new data, but trip header timestamp is still freshAt (frozen).
	hasNewData := manager.updateFeedRealtime(context.Background(), feed)
	require.True(t, hasNewData, "advancing vehicles count as partial success")

	// Frozen trips must age out and clear
	cleared := manager.clearStaleTripUpdates(feed.ID)
	assert.True(t, cleared, "frozen trips must age out when header timestamp does not advance")
	assert.Empty(t, manager.feedTrips[feed.ID], "frozen trip updates must be cleared")
	assert.NotEmpty(t, manager.GetRealTimeVehicles(), "fresh vehicles must survive")

	// One more poll: feed still returns the frozen trip payload. Because the
	// watermark survived clearStaleTripUpdates, the frozen payload is rejected
	// again and trips stay gone.
	manager.updateFeedRealtime(context.Background(), feed)
	assert.Empty(t, manager.feedTrips[feed.ID], "frozen trip updates must stay gone across subsequent polls")
}

// TestUpdateFeedRealtime_StaleTripSkipped verifies that an unchanged trip feed
// timestamp is skipped as stale, does not refresh feedTripLastSuccess, and does
// not log a failed update error.
func TestUpdateFeedRealtime_StaleTripSkipped(t *testing.T) {
	manager := newTestManager()
	ctx := context.Background()

	freshAt := time.Unix(1_700_000_000, 0).UTC()
	var mu sync.Mutex
	payload := encodeTripFeed(freshAt, "trip-1", "route-1")
	tripServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(payload)
	}))
	defer tripServer.Close()

	feed := RTFeedConfig{
		ID:             "stale-trip-feed",
		TripUpdatesURL: tripServer.URL,
	}

	assert.True(t, manager.updateFeedRealtime(ctx, feed), "first poll should store trips")
	require.Len(t, manager.GetRealTimeTrips(), 1)

	var buf bytes.Buffer
	staleCtx := logging.WithLogger(ctx, logging.NewStructuredLogger(&buf, slog.LevelDebug))
	assert.False(t, manager.updateFeedRealtime(staleCtx, feed), "an unchanged trip feed timestamp should not count as new data")
	trips := manager.GetRealTimeTrips()
	require.Len(t, trips, 1)
	assert.Equal(t, "trip-1", trips[0].ID.ID)
	logOutput := buf.String()
	assert.Contains(t, logOutput, "skipping_stale_trip_realtime_feed")
	assert.NotContains(t, logOutput, "realtime feed update failed")

	// When a newer timestamp arrives, it is applied
	newerAt := freshAt.Add(time.Minute)
	mu.Lock()
	payload = encodeTripFeed(newerAt, "trip-2", "route-1")
	mu.Unlock()

	assert.True(t, manager.updateFeedRealtime(ctx, feed), "a newer trip feed timestamp should count as new data")
	trips = manager.GetRealTimeTrips()
	require.Len(t, trips, 1)
	assert.Equal(t, "trip-2", trips[0].ID.ID)

}

// TestUpdateFeedRealtime_FutureTripHeaderThenClearAcceptsCorrectedHeader guards
// that a trip feed whose header sits in the future is applied without moving
// the watermark, so a later clock correction is accepted.
func TestUpdateFeedRealtime_FutureTripHeaderThenClearAcceptsCorrectedHeader(t *testing.T) {
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

	feed := RTFeedConfig{
		ID:             "trip-clock-correction",
		TripUpdatesURL: server.URL,
	}

	now := time.Now().UTC().Truncate(time.Second)
	futureAt := now.Add(time.Hour)
	mu.Lock()
	payload = encodeTripFeed(futureAt, "trip-future", "route-1")
	mu.Unlock()

	var buf bytes.Buffer
	futureCtx := logging.WithLogger(ctx, logging.NewStructuredLogger(&buf, slog.LevelDebug))
	assert.True(t, manager.updateFeedRealtime(futureCtx, feed), "a future trip header still publishes its trips")
	trips := manager.GetRealTimeTrips()
	require.Len(t, trips, 1)
	assert.Equal(t, "trip-future", trips[0].ID.ID)

	manager.realTimeMutex.RLock()
	assert.Zero(t, manager.feedTripTimestamp[feed.ID], "future trip header must not advance the watermark")
	manager.realTimeMutex.RUnlock()

	assert.Contains(t, buf.String(), "ignoring_future_trip_feed_timestamp")

	// Producer corrects clock back to normal now.
	mu.Lock()
	payload = encodeTripFeed(now, "trip-corrected", "route-1")
	mu.Unlock()

	assert.True(t, manager.updateFeedRealtime(ctx, feed), "corrected trip header must be accepted")
	trips = manager.GetRealTimeTrips()
	require.Len(t, trips, 1)
	assert.Equal(t, "trip-corrected", trips[0].ID.ID)
}
