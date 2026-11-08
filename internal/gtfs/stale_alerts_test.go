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

// TestClearStaleAlerts guards #1982: service alerts must age out on their own
// sub-feed clock, because a partial success from a sibling sub-feed keeps
// resetting the feed-wide staleness timer, so clearFeedData never runs.
func TestClearStaleAlerts(t *testing.T) {
	stale := time.Now().Add(-staleFeedThreshold - time.Minute)

	tests := []struct {
		name          string
		lastSuccess   map[string]time.Time
		alerts        map[string][]gtfs.Alert
		expectCleared bool
	}{
		{
			name:          "alerts older than threshold are cleared",
			lastSuccess:   map[string]time.Time{"feed": stale},
			alerts:        map[string][]gtfs.Alert{"feed": {{ID: "a1"}}},
			expectCleared: true,
		},
		{
			name:          "alerts newer than threshold are kept",
			lastSuccess:   map[string]time.Time{"feed": time.Now()},
			alerts:        map[string][]gtfs.Alert{"feed": {{ID: "a1"}}},
			expectCleared: false,
		},
		{
			name:          "feed that never succeeded an alerts fetch has nothing to clear",
			alerts:        map[string][]gtfs.Alert{},
			expectCleared: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{
				feedAlerts:           test.alerts,
				feedAlertLastSuccess: test.lastSuccess,
			}
			manager.rebuildMergedRealtimeLocked()

			cleared := manager.clearStaleAlerts("feed")

			assert.Equal(t, test.expectCleared, cleared)
			if test.expectCleared {
				assert.Empty(t, manager.feedAlerts["feed"])
			} else {
				assert.Equal(t, test.alerts["feed"], manager.feedAlerts["feed"])
			}
		})
	}
}

// TestStaleAlertsClearedWhileVehiclesKeepSucceeding reproduces the #1982
// poller tick at update level: the service-alerts URL fails while the
// vehicle-positions URL succeeds, so the update reports partial success —
// exactly the case that resets the feed-wide timer on main. The per-tick
// clearStaleAlerts call (same order as pollFeed) must drop the aged alerts
// and keep the fresh vehicles.
func TestStaleAlertsClearedWhileVehiclesKeepSucceeding(t *testing.T) {
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

	alertServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service alerts unavailable", http.StatusInternalServerError)
	}))
	defer alertServer.Close()

	feed := RTFeedConfig{
		ID:                  "partial-feed",
		ServiceAlertsURL:    alertServer.URL,
		VehiclePositionsURL: vehicleServer.URL,
	}

	// Seed as if alerts last succeeded long ago, then the service-alerts URL
	// started failing.
	manager.realTimeMutex.Lock()
	manager.feedAlerts[feed.ID] = []gtfs.Alert{{ID: "stale-alert"}}
	manager.feedAlertLastSuccess = map[string]time.Time{feed.ID: time.Now().Add(-staleFeedThreshold - time.Minute)}
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	// pollFeed order: update, then age out stale sub-feed data.
	hasNewData := manager.updateFeedRealtime(context.Background(), feed)
	require.True(t, hasNewData, "vehicle success must still count as partial success")
	require.True(t, manager.clearStaleAlerts(feed.ID), "stale alerts must age out on their own clock")

	assert.Empty(t, manager.feedAlerts[feed.ID], "stale alerts must not survive a failing service-alerts URL")
	assert.NotEmpty(t, manager.GetRealTimeVehicles(), "fresh vehicles must survive")
}