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

func tripOnRoute(tripID, routeID string) gtfs.Trip {
	return gtfs.Trip{ID: gtfs.TripID{ID: tripID, RouteID: routeID}, IsEntityInMessage: true}
}

func tripIDs(trips []gtfs.Trip) []string {
	ids := make([]string, 0, len(trips))
	for _, trip := range trips {
		ids = append(ids, trip.ID.ID)
	}
	return ids
}

func TestAttributeTrips(t *testing.T) {
	// newAttributionTestManager: R1 is agency-A, R2 is agency-C, static T1 runs on R1.
	trips := []gtfs.Trip{
		tripOnRoute("T9", "R1"),
		tripOnRoute("T1", ""),
		tripOnRoute("T1", "X"),
		tripOnRoute("T8", "R2"),
		tripOnRoute("T7", "X"),
	}

	t.Run("feed without agency-ids keeps every trip", func(t *testing.T) {
		manager := newAttributionTestManager(t)

		got, err := manager.attributeTrips(context.Background(), trips, RTFeedConfig{ID: "feed"})
		require.NoError(t, err)

		assert.Equal(t, []string{"T9", "T1", "T1", "T8", "T7"}, tripIDs(got.kept))
		assert.Empty(t, got.unattributed)
		assert.Equal(t, map[string]string{"T9": "agency-A", "T1": "agency-A", "T8": "agency-C"}, got.agencies,
			"T7 resolves by neither route nor static trip")
	})

	t.Run("static trip fills a missing route only", func(t *testing.T) {
		manager := newAttributionTestManager(t)

		got, err := manager.attributeTrips(context.Background(), trips, RTFeedConfig{ID: "feed"})
		require.NoError(t, err)

		assert.Equal(t, "R1", got.kept[1].ID.RouteID)
		assert.Equal(t, "X", got.kept[2].ID.RouteID, "an unknown route_id is kept as ingested")
		assert.Empty(t, trips[1].ID.RouteID, "the parsed feed trip must not be changed in place")
	})

	t.Run("feed with agency-ids", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		feedCfg := RTFeedConfig{ID: "feed", AgencyIDs: []string{"agency-B", "agency-A"}}
		manager.feedAgencyFilter = Config{RTFeeds: []RTFeedConfig{{ID: "feed", AgencyIDs: feedCfg.AgencyIDs, TripUpdatesURL: "x", Enabled: true}}}.feedAgencyFilters()

		got, err := manager.attributeTrips(context.Background(), trips, feedCfg)
		require.NoError(t, err)

		assert.Equal(t, []string{"T9", "T1", "T1"}, tripIDs(got.kept), "T8 runs another agency's route")
		assert.Equal(t, []string{"T7"}, tripIDs(got.unattributed))
		assert.Equal(t, map[string]string{"T9": "agency-A", "T1": "agency-A", "T7": "agency-B"}, got.agencies,
			"an unresolvable trip falls back to the feed's first agency")
	})
}

func TestAttributeTripsLookupError(t *testing.T) {
	manager := newAttributionTestManager(t)
	require.NoError(t, manager.GtfsDB.DB.Close())

	_, err := manager.attributeTrips(context.Background(), []gtfs.Trip{tripOnRoute("T1", "")}, RTFeedConfig{ID: "feed"})
	require.Error(t, err)
}

// serveTripFeed serves one GTFS-RT message of trip updates, each on the
// given trip and route, from a test server.
func serveTripFeed(t *testing.T, feedCfg RTFeedConfig, createdAt time.Time, trips ...gtfs.TripID) RTFeedConfig {
	t.Helper()
	feed := &gtfsrt.FeedMessage{Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")}}
	if !createdAt.IsZero() {
		feed.Header.Timestamp = proto.Uint64(uint64(createdAt.Unix()))
	}
	for _, trip := range trips {
		descriptor := &gtfsrt.TripDescriptor{TripId: proto.String(trip.ID)}
		if trip.RouteID != "" {
			descriptor.RouteId = proto.String(trip.RouteID)
		}
		feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
			Id:         proto.String(trip.ID),
			TripUpdate: &gtfsrt.TripUpdate{Trip: descriptor},
		})
	}
	payload, err := proto.Marshal(feed)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	feedCfg.TripUpdatesURL = server.URL
	feedCfg.RefreshInterval = 30
	feedCfg.Enabled = true
	return feedCfg
}

func TestTripAttributionIngestion(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Now().Add(-time.Minute).Truncate(time.Second)

	t.Run("stores agencies and the feed time", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		feed := serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt, gtfs.TripID{ID: "T1"}, gtfs.TripID{ID: "T8", RouteID: "R2"})

		require.True(t, manager.updateFeedRealtime(ctx, feed))

		assert.Equal(t, map[string]string{"T1": "agency-A", "T8": "agency-C"}, manager.feedTripAgency["feed"])
		assert.True(t, createdAt.Equal(manager.feedTripDataTime["feed"]))
		assert.Equal(t, "R1", manager.feedTrips["feed"][0].ID.RouteID)
	})

	t.Run("feed without a header time is dated at the fetch", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		feed := serveTripFeed(t, RTFeedConfig{ID: "feed"}, time.Time{}, gtfs.TripID{ID: "T1"})
		before := time.Now()

		require.True(t, manager.updateFeedRealtime(ctx, feed))

		assert.False(t, manager.feedTripDataTime["feed"].Before(before))
	})

	t.Run("a refresh replaces the attribution", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		require.True(t, manager.updateFeedRealtime(ctx, serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt, gtfs.TripID{ID: "T1"})))

		require.True(t, manager.updateFeedRealtime(ctx, serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt, gtfs.TripID{ID: "T8", RouteID: "R2"})))

		assert.Equal(t, map[string]string{"T8": "agency-C"}, manager.feedTripAgency["feed"])
	})

	t.Run("lookup failure in a feed without agency-ids keeps the trips", func(t *testing.T) {
		manager := newTestManager()
		feed := serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt, gtfs.TripID{ID: "T1", RouteID: "R1"})

		require.True(t, manager.updateFeedRealtime(ctx, feed))

		assert.Len(t, manager.feedTrips["feed"], 1)
		assert.Empty(t, manager.feedTripAgency["feed"])
	})

	t.Run("lookup failure keeps each trip's last known agency", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		require.True(t, manager.updateFeedRealtime(ctx, serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt,
			gtfs.TripID{ID: "T1"}, gtfs.TripID{ID: "T8", RouteID: "R2"})))
		require.NoError(t, manager.GtfsDB.DB.Close())

		require.True(t, manager.updateFeedRealtime(ctx, serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt, gtfs.TripID{ID: "T1"})))

		assert.Equal(t, map[string]string{"T1": "agency-A"}, manager.feedTripAgency["feed"],
			"T8 left the feed, so its agency is dropped")
		assert.Len(t, manager.TripUpdatesForAgency("agency-A").TripUpdates, 1)
	})

	t.Run("clearing the feed drops its attribution", func(t *testing.T) {
		manager := newAttributionTestManager(t)
		require.True(t, manager.updateFeedRealtime(ctx, serveTripFeed(t, RTFeedConfig{ID: "feed"}, createdAt, gtfs.TripID{ID: "T1"})))

		manager.clearFeedData("feed")

		assert.NotContains(t, manager.feedTripAgency, "feed")
		assert.NotContains(t, manager.feedTripDataTime, "feed")
	})
}
