package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
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
	require.Equal(t, first, manager.Alerts()[0].CreatedAt, "an unchanged poll must preserve creation time")
	reordered := alert
	reordered.InformedEntities = slices.Clone(alert.InformedEntities)
	slices.Reverse(reordered.InformedEntities)
	publish("a", []gtfs.Alert{reordered}, third)
	require.Equal(t, first, manager.Alerts()[0].CreatedAt, "selector order alone must not refresh creation time")
	require.Equal(t, reordered.InformedEntities, manager.Alerts()[0].InformedEntities, "retain incoming selector order")
	changedSelectors := reordered
	changedSelectors.InformedEntities = slices.Clone(reordered.InformedEntities)
	changedSelectors.InformedEntities[0] = changedSelectors.InformedEntities[1]
	publish("a", []gtfs.Alert{changedSelectors}, third)
	require.Equal(t, third, manager.Alerts()[0].CreatedAt, "replacing a selector must refresh creation time")
	edited := alert
	edited.Header = []gtfs.AlertText{{Text: "Edited"}}
	publish("a", []gtfs.Alert{edited}, third.Add(time.Hour))
	require.Equal(t, third.Add(time.Hour), manager.Alerts()[0].CreatedAt)
	require.Equal(t, second, manager.Alerts()[1].CreatedAt, "another feed's timestamp must not advance")
	require.Equal(t, first, old[0].CreatedAt, "previously published snapshots must not change")
	manager.clearFeedData("a")
	require.NotContains(t, manager.feedAlertCreatedAt, "a")
	require.Len(t, manager.Alerts(), 1)
	require.Equal(t, second, manager.Alerts()[0].CreatedAt)
	publish("b", nil, third)
	require.Empty(t, manager.Alerts(), "a successful empty feed removes the old alert")
	publish("b", []gtfs.Alert{alert}, third)
	require.Equal(t, third, manager.Alerts()[0].CreatedAt, "an alert returning after removal gets a new timestamp")
	manager.MockResetRealTimeData()
	require.Empty(t, manager.Alerts())
	require.Empty(t, manager.feedAlertCreatedAt)
	require.NotNil(t, manager.feedAlertCreatedAt)
	publish("b", []gtfs.Alert{alert}, first)
	require.Len(t, manager.Alerts(), 1)
	require.Equal(t, first, manager.Alerts()[0].CreatedAt, "reset must leave the feed maps ready for reuse")
}

// TestAlertCreationTimeFetchFailure uses real HTTP/protobuf ingestion to check
// failed fetches and unchanged polls preserve timestamps; edited content refreshes one.
func TestAlertCreationTimeFetchFailure(t *testing.T) {
	makePayload := func(timestamp uint64, text string) []byte {
		t.Helper()
		entity := func(id, header string) *gtfsrt.FeedEntity {
			return &gtfsrt.FeedEntity{Id: proto.String(id), Alert: &gtfsrt.Alert{
				InformedEntity: []*gtfsrt.EntitySelector{{AgencyId: proto.String("25")}},
				HeaderText:     &gtfsrt.TranslatedString{Translation: []*gtfsrt.TranslatedString_Translation{{Text: proto.String(header)}}},
			}}
		}
		payload, err := proto.Marshal(&gtfsrt.FeedMessage{
			Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(timestamp)},
			Entity: []*gtfsrt.FeedEntity{entity("alert", text), entity("other", "Unchanged")},
		})
		require.NoError(t, err)
		return payload
	}
	payload := makePayload(1, "Original")
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
	require.Len(t, got, 2)
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
	poll := func(payload []byte) []FeedAlert {
		t.Helper()
		mu.Lock()
		status, body = http.StatusOK, payload
		mu.Unlock()
		require.True(t, manager.updateFeedRealtime(context.Background(), feed))
		alerts := manager.Alerts()
		require.Len(t, alerts, 2)
		return alerts
	}
	for _, unchanged := range [][]byte{payload, makePayload(2, "Original")} {
		recovered := poll(unchanged)
		require.Equal(t, old, recovered[0].CreatedAt, "unchanged content keeps its timestamp")
		require.Equal(t, got[1].CreatedAt, recovered[1].CreatedAt)
	}
	edited := poll(makePayload(3, "Edited"))
	require.Equal(t, "alert", edited[0].ID)
	require.Equal(t, "Edited", edited[0].Header[0].Text)
	require.True(t, edited[0].CreatedAt.After(old), "edited content gets a new timestamp")
	require.Equal(t, "other", edited[1].ID)
	require.Equal(t, got[1].CreatedAt, edited[1].CreatedAt, "editing one alert leaves the other unchanged")
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
