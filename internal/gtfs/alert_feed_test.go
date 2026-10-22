package gtfs

import (
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// newAlertFeedTestManager returns a manager whose configuration lists the
// given alert feeds, in order, each with its agency-ids.
func newAlertFeedTestManager(feeds ...RTFeedConfig) *Manager {
	manager := newTestManager()
	for _, feed := range feeds {
		feed.ServiceAlertsURL = "x"
		feed.Enabled = true
		manager.config.RTFeeds = append(manager.config.RTFeeds, feed)
	}
	return manager
}

func setFeedAlerts(manager *Manager, feedID string, alerts ...gtfs.Alert) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()
	manager.feedAlerts[feedID] = alerts
	manager.rebuildMergedRealtimeLocked()
}

func decodeAlertFeed(t *testing.T, manager *Manager, agencyID string) *gtfsrt.FeedMessage {
	t.Helper()
	feed := &gtfsrt.FeedMessage{}
	body, err := manager.AlertFeedForAgency(agencyID)
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(body, feed))
	return feed
}

func alertEntityIDs(feed *gtfsrt.FeedMessage) []string {
	ids := []string{}
	for _, entity := range feed.GetEntity() {
		ids = append(ids, entity.GetId())
	}
	return ids
}

func agencySelector(agencyID string) gtfs.AlertInformedEntity {
	return gtfs.AlertInformedEntity{AgencyID: &agencyID}
}

func routeSelector(routeID string) gtfs.AlertInformedEntity {
	return gtfs.AlertInformedEntity{RouteID: &routeID}
}

func headedAlert(alertID, header string, entities ...gtfs.AlertInformedEntity) gtfs.Alert {
	return gtfs.Alert{
		ID:               alertID,
		Header:           []gtfs.AlertText{{Text: header, Language: "en"}},
		InformedEntities: entities,
	}
}

func TestAlertFeedForAgency_Attribution(t *testing.T) {
	type feedAlerts struct {
		feed   RTFeedConfig
		alerts []gtfs.Alert
	}
	tests := []struct {
		name  string
		feeds []feedAlerts
		want  map[string][]string
	}{
		{
			name: "rule 1: the alert's own agency_id wins over the feed's",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1", AgencyIDs: []string{"1"}},
				alerts: []gtfs.Alert{headedAlert("a", "x", agencySelector("40"))},
			}},
			want: map[string][]string{"40": {"40_a"}, "1": {}},
		},
		{
			name: "rule 1 skips an empty agency_id",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1"},
				alerts: []gtfs.Alert{headedAlert("a", "x", agencySelector(""), agencySelector("40"))},
			}},
			want: map[string][]string{"40": {"40_a"}},
		},
		{
			name: "rule 2: a route-only alert gets the feed's first agency",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1", AgencyIDs: []string{"1", "3"}},
				alerts: []gtfs.Alert{headedAlert("a", "x", routeSelector("100"))},
			}},
			want: map[string][]string{"1": {"1_a"}, "3": {}},
		},
		{
			name: "rule 2 applies to an alert with no selectors",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1", AgencyIDs: []string{"1"}},
				alerts: []gtfs.Alert{headedAlert("a", "x")},
			}},
			want: map[string][]string{"1": {"1_a"}},
		},
		{
			name: "rule 3: a route-only alert in a feed without agency-ids is in no agency",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1"},
				alerts: []gtfs.Alert{headedAlert("a", "x", routeSelector("100"))},
			}},
			want: map[string][]string{"1": {}, "": {}},
		},
		{
			name: "an alert without an ID is not exported",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1", AgencyIDs: []string{"1"}},
				alerts: []gtfs.Alert{headedAlert("", "x", agencySelector("1"))},
			}},
			want: map[string][]string{"1": {}},
		},
		{
			name: "an ID that already carries the prefix is not prefixed again",
			feeds: []feedAlerts{{
				feed: RTFeedConfig{ID: "f1"},
				alerts: []gtfs.Alert{
					headedAlert("92239", "x", agencySelector("1")),
					headedAlert("1_5", "x", agencySelector("1")),
				},
			}},
			want: map[string][]string{"1": {"1_5", "1_92239"}},
		},
		{
			name: "entities are sorted by ID",
			feeds: []feedAlerts{{
				feed:   RTFeedConfig{ID: "f1", AgencyIDs: []string{"1"}},
				alerts: []gtfs.Alert{headedAlert("b", "x"), headedAlert("a", "x")},
			}},
			want: map[string][]string{"1": {"1_a", "1_b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var configs []RTFeedConfig
			for _, feed := range tt.feeds {
				configs = append(configs, feed.feed)
			}
			manager := newAlertFeedTestManager(configs...)
			for _, feed := range tt.feeds {
				setFeedAlerts(manager, feed.feed.ID, feed.alerts...)
			}
			for agencyID, wantIDs := range tt.want {
				assert.Equal(t, wantIDs, alertEntityIDs(decodeAlertFeed(t, manager, agencyID)), "agency %q", agencyID)
			}
		})
	}
}

func TestAlertFeedForAgency_Duplicates(t *testing.T) {
	t.Run("the feed listed first in config wins", func(t *testing.T) {
		manager := newAlertFeedTestManager(RTFeedConfig{ID: "z-first"}, RTFeedConfig{ID: "a-second"})
		setFeedAlerts(manager, "a-second", headedAlert("a", "second", agencySelector("1")))
		setFeedAlerts(manager, "z-first", headedAlert("a", "first", agencySelector("1")))

		feed := decodeAlertFeed(t, manager, "1")
		require.Len(t, feed.GetEntity(), 1)
		assert.Equal(t, "first", feed.GetEntity()[0].GetAlert().GetHeaderText().GetTranslation()[0].GetText())
	})
	t.Run("the first copy within one feed wins", func(t *testing.T) {
		manager := newAlertFeedTestManager(RTFeedConfig{ID: "f1"})
		setFeedAlerts(manager, "f1",
			headedAlert("a", "first", agencySelector("1")),
			headedAlert("a", "second", agencySelector("1")))

		feed := decodeAlertFeed(t, manager, "1")
		require.Len(t, feed.GetEntity(), 1)
		assert.Equal(t, "first", feed.GetEntity()[0].GetAlert().GetHeaderText().GetTranslation()[0].GetText())
	})
}

func TestAlertFeedForAgency_AlertContent(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	end := time.Unix(1_700_003_600, 0)
	agencyID, routeID, stopID := "1", "100", "S1"
	alert := gtfs.Alert{
		ID:     "a",
		Cause:  gtfs.Strike,
		Effect: gtfs.NoService,
		Header: []gtfs.AlertText{{Text: "Closed", Language: "en"}, {Text: "Cerrado", Language: "es"}},
		URL:    []gtfs.AlertText{{Text: "https://example.com"}},
		ActivePeriods: []gtfs.AlertActivePeriod{
			{StartsAt: &start},
			{EndsAt: &end},
			{},
		},
		InformedEntities: []gtfs.AlertInformedEntity{
			{AgencyID: &agencyID},
			{RouteID: &routeID, RouteType: gtfs.RouteType_Bus},
			{TripID: &gtfs.TripID{ID: "T1", RouteID: "100"}},
			{StopID: &stopID},
			agencySelector(""),
		},
	}
	manager := newAlertFeedTestManager(RTFeedConfig{ID: "f1"})
	setFeedAlerts(manager, "f1", alert)

	feed := decodeAlertFeed(t, manager, "1")
	require.Len(t, feed.GetEntity(), 1)
	exported := feed.GetEntity()[0].GetAlert()

	t.Run("texts keep every translation and absent fields stay absent", func(t *testing.T) {
		translations := exported.GetHeaderText().GetTranslation()
		require.Len(t, translations, 2)
		assert.Equal(t, "Closed", translations[0].GetText())
		assert.Equal(t, "en", translations[0].GetLanguage())
		assert.Equal(t, "Cerrado", translations[1].GetText())
		assert.Nil(t, exported.DescriptionText)
		require.Len(t, exported.GetUrl().GetTranslation(), 1)
		assert.Nil(t, exported.GetUrl().GetTranslation()[0].Language, "an empty language is left unset")
	})
	t.Run("active periods keep their set bounds, and an empty one stays always active", func(t *testing.T) {
		periods := exported.GetActivePeriod()
		require.Len(t, periods, 3)
		assert.Equal(t, uint64(start.Unix()), periods[0].GetStart())
		assert.Nil(t, periods[0].End)
		assert.Nil(t, periods[1].Start)
		assert.Equal(t, uint64(end.Unix()), periods[1].GetEnd())
		assert.Nil(t, periods[2].Start, "a range with neither bound means always active")
		assert.Nil(t, periods[2].End)
	})
	t.Run("selectors keep only agency, route, trip_id and stop, and an empty one is dropped", func(t *testing.T) {
		// The selectors after the agency one are sorted by route, trip, stop.
		selectors := exported.GetInformedEntity()
		require.Len(t, selectors, 4)
		assert.Equal(t, "1", selectors[0].GetAgencyId())
		assert.Equal(t, "S1", selectors[1].GetStopId())
		assert.Equal(t, "T1", selectors[2].GetTrip().GetTripId())
		assert.Nil(t, selectors[2].GetTrip().RouteId)
		assert.Equal(t, "100", selectors[3].GetRouteId())
		assert.Nil(t, selectors[3].RouteType)
	})
	t.Run("cause and effect are not sent", func(t *testing.T) {
		assert.Nil(t, exported.Cause)
		assert.Nil(t, exported.Effect)
	})
	t.Run("the header is a full GTFS-RT 2.0 dataset", func(t *testing.T) {
		assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
		assert.Nil(t, feed.GetHeader().Incrementality)
	})
}

func TestAlertFeedForAgency_ChangeTime(t *testing.T) {
	alertFeedOf := func(manager *Manager, agencyID string) agencyAlertFeed {
		return manager.mergedRealtime().alertFeedsByAgency[agencyID]
	}
	newManager := func() *Manager {
		manager := newAlertFeedTestManager(RTFeedConfig{ID: "f1", AgencyIDs: []string{"1"}})
		setFeedAlerts(manager, "f1", headedAlert("a", "first"))
		return manager
	}

	t.Run("an unchanged alert set keeps its bytes and change time", func(t *testing.T) {
		manager := newManager()
		before := alertFeedOf(manager, "1")
		setFeedAlerts(manager, "f1", headedAlert("a", "first"))
		assert.Equal(t, before, alertFeedOf(manager, "1"))
	})
	t.Run("a vehicle-only rebuild leaves the bytes unchanged", func(t *testing.T) {
		manager := newManager()
		before := alertFeedOf(manager, "1")
		seedAgencyFeed(manager, "vehicles", "1", time.Now(), time.Now(), exportVehicle("V1", time.Now()))
		rebuild(manager)
		assert.Equal(t, before, alertFeedOf(manager, "1"))
	})
	t.Run("a changed alert set gets the new build time", func(t *testing.T) {
		manager := newManager()
		setFeedAlerts(manager, "f1", headedAlert("a", "edited"))
		merged := manager.mergedRealtime()
		assert.Equal(t, merged.builtAt, merged.alertFeedsByAgency["1"].changedAt)
		assert.Equal(t, uint64(merged.builtAt.Unix()), decodeAlertFeed(t, manager, "1").GetHeader().GetTimestamp())
	})
	t.Run("removing the agency's last alert leaves a header-only feed with a new time", func(t *testing.T) {
		manager := newManager()
		setFeedAlerts(manager, "f1")
		merged := manager.mergedRealtime()
		assert.Equal(t, merged.builtAt, merged.alertFeedsByAgency["1"].changedAt)
		assert.Empty(t, decodeAlertFeed(t, manager, "1").GetEntity())
	})
	t.Run("a never-seen agency is dated at the first snapshot, stable over rebuilds", func(t *testing.T) {
		manager := newManager()
		firstBuild := manager.mergedRealtime().builtAt
		before, err := manager.AlertFeedForAgency("nope")
		require.NoError(t, err)
		setFeedAlerts(manager, "f1", headedAlert("a", "edited"))

		after, err := manager.AlertFeedForAgency("nope")
		require.NoError(t, err)
		assert.Equal(t, before, after)
		feed := decodeAlertFeed(t, manager, "nope")
		assert.Empty(t, feed.GetEntity())
		assert.Equal(t, uint64(firstBuild.Unix()), feed.GetHeader().GetTimestamp())
	})
	t.Run("before any snapshot the timestamp is the current time", func(t *testing.T) {
		before := time.Now().Unix()
		feed := decodeAlertFeed(t, newTestManager(), "1")
		assert.GreaterOrEqual(t, int64(feed.GetHeader().GetTimestamp()), before)
	})
	t.Run("clone keeps the alert feeds and their start time", func(t *testing.T) {
		manager := newManager()
		merged := manager.mergedRealtime()
		cloned := merged.clone()
		assert.Equal(t, merged.alertFeedsByAgency, cloned.alertFeedsByAgency)
		assert.Equal(t, merged.alertsSince, cloned.alertsSince)
	})
	t.Run("the same alerts give identical bytes in two managers", func(t *testing.T) {
		first, second := newManager(), newManager()
		alerts := []gtfs.Alert{
			headedAlert("b", "x", agencySelector("1"), routeSelector("100")),
			headedAlert("a", "y"),
		}
		setFeedAlerts(first, "f1", alerts...)
		setFeedAlerts(second, "f1", alerts...)
		firstFeed, secondFeed := decodeAlertFeed(t, first, "1"), decodeAlertFeed(t, second, "1")
		firstFeed.Header, secondFeed.Header = nil, nil
		assert.True(t, proto.Equal(firstFeed, secondFeed))
	})
}

// go-gtfs adds a route selector for each route-only trip descriptor in map
// order, so the same upstream bytes can parse into a different order.
func TestAlertFeedForAgency_StableAcrossReparses(t *testing.T) {
	selectors := []*gtfsrt.EntitySelector{{AgencyId: proto.String("1")}}
	for _, routeID := range []string{"A", "B", "C", "D", "E", "F"} {
		selectors = append(selectors, &gtfsrt.EntitySelector{Trip: &gtfsrt.TripDescriptor{RouteId: proto.String(routeID)}})
	}
	upstream, err := proto.Marshal(&gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")},
		Entity: []*gtfsrt.FeedEntity{{Id: proto.String("a"), Alert: &gtfsrt.Alert{InformedEntity: selectors}}},
	})
	require.NoError(t, err)

	manager := newAlertFeedTestManager(RTFeedConfig{ID: "f1"})
	var first []byte
	for range 20 {
		parsed, err := gtfs.ParseRealtime(upstream, &gtfs.ParseRealtimeOptions{})
		require.NoError(t, err)
		setFeedAlerts(manager, "f1", parsed.Alerts...)
		body, err := manager.AlertFeedForAgency("1")
		require.NoError(t, err)
		if first == nil {
			first = body
			continue
		}
		require.Equal(t, first, body, "an unchanged upstream alert must keep its bytes")
	}
	feed := decodeAlertFeed(t, manager, "1")
	assert.Equal(t, "1", feed.GetEntity()[0].GetAlert().GetInformedEntity()[0].GetAgencyId(),
		"the alert's own agency stays the first selector")
}
