package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestAlertCreationTimeSnapshots pins per-feed identity and immutable timestamps
// across re-ingestion, unrelated rebuilds, removals and repeated indexed reads.
func TestAlertCreationTimeSnapshots(t *testing.T) {
	manager := newTestManager()
	first := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	agency, stop, route, trip := "25", "S", "R", "T"
	alert := gtfs.Alert{ID: "dup", InformedEntities: []gtfs.AlertInformedEntity{
		{AgencyID: &agency}, {StopID: &stop}, {RouteID: &route}, {TripID: &gtfs.TripID{ID: trip}},
	}}
	publish := func(id string, alerts []gtfs.Alert, at time.Time) {
		manager.realTimeMutex.Lock()
		defer manager.realTimeMutex.Unlock()
		manager.storeFeedAlerts(id, alerts, at)
		manager.rebuildMergedRealtimeLocked()
	}
	publish("a", []gtfs.Alert{alert}, first)
	publish("b", []gtfs.Alert{alert}, second)
	old := manager.Alerts()
	require.Len(t, old, 2)
	require.Equal(t, first, old[0].CreatedAt)
	require.Equal(t, second, old[1].CreatedAt)
	for _, read := range []func() []FeedAlert{
		manager.Alerts,
		func() []FeedAlert { return manager.GetAlertsByIDs(trip, route, agency) },
		func() []FeedAlert { return manager.GetAlertsForStop(stop, agency) },
		func() []FeedAlert { return manager.GetAlertsForRoute(route) },
	} {
		for range 3 {
			got := read()
			require.NotEmpty(t, got)
			require.Equal(t, first, got[0].CreatedAt)
		}
	}
	third := second.Add(time.Hour)
	publish("a", []gtfs.Alert{alert}, third)
	require.Equal(t, third, manager.Alerts()[0].CreatedAt)
	require.Equal(t, second, manager.Alerts()[1].CreatedAt, "another feed's timestamp must not advance")
	require.Equal(t, first, old[0].CreatedAt, "previously published snapshots must not change")
	manager.clearFeedData("a")
	require.NotContains(t, manager.feedAlertIngestedAt, "a")
	require.Len(t, manager.Alerts(), 1)
	require.Equal(t, second, manager.Alerts()[0].CreatedAt)
	publish("b", nil, third)
	require.Empty(t, manager.Alerts(), "a successful empty feed removes the old alert")
	publish("b", []gtfs.Alert{alert}, third)
	manager.MockResetRealTimeData()
	require.Empty(t, manager.Alerts())
	require.Empty(t, manager.feedAlertIngestedAt)
	require.NotNil(t, manager.feedAlertIngestedAt)
	publish("b", []gtfs.Alert{alert}, first)
	require.Len(t, manager.Alerts(), 1)
	require.Equal(t, first, manager.Alerts()[0].CreatedAt, "reset must leave the feed maps ready for reuse")
}

// TestAlertCreationTimeFetchFailure uses real HTTP/protobuf ingestion to check
// failed and canceled fetches preserve the timestamp, then recovery refreshes it.
func TestAlertCreationTimeFetchFailure(t *testing.T) {
	payload, err := proto.Marshal(&gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(1)},
		Entity: []*gtfsrt.FeedEntity{{Id: proto.String("alert"), Alert: &gtfsrt.Alert{
			InformedEntity: []*gtfsrt.EntitySelector{{AgencyId: proto.String("25")}},
		}}},
	})
	require.NoError(t, err)
	var mu sync.Mutex
	status, body := http.StatusOK, payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	manager := newTestManager()
	feed := RTFeedConfig{ID: "a", ServiceAlertsURL: server.URL}
	before := time.Now()
	require.True(t, manager.updateFeedRealtime(context.Background(), feed))
	got := manager.Alerts()
	require.Len(t, got, 1)
	require.False(t, got[0].CreatedAt.Before(before))
	require.False(t, got[0].CreatedAt.After(time.Now()))
	old := got[0].CreatedAt
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
	}{
		{"http", http.StatusServiceUnavailable, nil},
		{"protobuf", http.StatusOK, []byte("not protobuf")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			status, body = tc.status, tc.body
			mu.Unlock()
			require.False(t, manager.updateFeedRealtime(context.Background(), feed))
			manager.realTimeMutex.Lock()
			manager.rebuildMergedRealtimeLocked()
			manager.realTimeMutex.Unlock()
			require.Equal(t, old, manager.Alerts()[0].CreatedAt)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, manager.updateFeedRealtime(ctx, feed))
	require.Equal(t, old, manager.Alerts()[0].CreatedAt)
	mu.Lock()
	status, body = http.StatusOK, payload
	mu.Unlock()
	require.True(t, manager.updateFeedRealtime(context.Background(), feed))
	recovered := manager.Alerts()
	require.Len(t, recovered, 1)
	require.True(t, recovered[0].CreatedAt.After(old), "every successful poll must restamp the alert")
}

// TestAlertCreationTimeConcurrentReads exercises timestamp publication while
// readers keep snapshots, without reading the mutable per-feed timestamp map.
func TestAlertCreationTimeConcurrentReads(t *testing.T) {
	manager := newTestManager()
	agency := "25"
	alert := gtfs.Alert{ID: "alert", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency}}}
	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	publishAt := func(at time.Time) {
		manager.realTimeMutex.Lock()
		defer manager.realTimeMutex.Unlock()
		manager.storeFeedAlerts("a", []gtfs.Alert{alert}, at)
		manager.rebuildMergedRealtimeLocked()
	}
	publishAt(base)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := range 32 {
			publishAt(base.Add(time.Duration(i) * time.Minute))
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 256 {
			alerts := manager.Alerts()
			if len(alerts) != 1 || alerts[0].CreatedAt.Before(base) || alerts[0].CreatedAt.After(base.Add(31*time.Minute)) {
				t.Errorf("invalid published timestamp snapshot: %v", alerts)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}
