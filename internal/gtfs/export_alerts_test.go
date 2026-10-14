package gtfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func exportAlertWithText(recordAgencyID, rawID, text string) ExportAlert {
	return ExportAlert{
		RecordAgencyID: recordAgencyID,
		Alert: gtfs.Alert{
			ID:     rawID,
			Header: []gtfs.AlertText{{Text: text, Language: "en"}},
		},
	}
}

func deletedExportAlert(recordAgencyID, rawID string) ExportAlert {
	alert := exportAlertWithText(recordAgencyID, rawID, "")
	alert.Alert.IsDeleted = true
	return alert
}

type alertRefresh struct {
	feedID string
	alerts []ExportAlert
}

func applyAlertRefreshes(manager *Manager, refreshes ...alertRefresh) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()
	for _, refresh := range refreshes {
		manager.applyExportAlertRefreshLocked(refresh.feedID, refresh.alerts)
	}
	manager.rebuildMergedRealtimeLocked()
}

// exportAlertTexts maps "{record agency}/{raw ID}" to header text for
// identified alerts, and counts anonymous ones under their text.
func exportAlertTexts(manager *Manager) (identified map[string]string, anonymous []string) {
	identified = make(map[string]string)
	for _, alert := range manager.ExportAlerts() {
		text := alert.Alert.Header[0].Text
		if alert.Alert.ID == "" {
			anonymous = append(anonymous, text)
			continue
		}
		identified[alert.RecordAgencyID+"/"+alert.Alert.ID] = text
	}
	return identified, anonymous
}

func TestApplyExportAlertRefreshLifecycle(t *testing.T) {
	tests := []struct {
		name          string
		refreshes     []alertRefresh
		wantAlerts    map[string]string
		wantAnonymous []string
	}{
		{
			name: "last applied record replaces the whole record",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "from A")}},
				{"B", []ExportAlert{exportAlertWithText("40", "notice", "from B")}},
			},
			wantAlerts: map[string]string{"40/notice": "from B"},
		},
		{
			name: "same raw ID in two record agencies stays distinct",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "agency 40")}},
				{"B", []ExportAlert{exportAlertWithText("20", "notice", "agency 20")}},
			},
			wantAlerts: map[string]string{"40/notice": "agency 40", "20/notice": "agency 20"},
		},
		{
			name: "anonymous alerts stay separate even with identical text",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "", "same"), exportAlertWithText("40", "", "same")}},
			},
			wantAlerts:    map[string]string{},
			wantAnonymous: []string{"same", "same"},
		},
		{
			name: "winning source omission removes the alert without reviving the older copy",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "from A")}},
				{"B", []ExportAlert{exportAlertWithText("40", "notice", "from B")}},
				{"B", nil},
			},
			wantAlerts: map[string]string{},
		},
		{
			name: "losing source omission leaves the winner",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "from A")}},
				{"B", []ExportAlert{exportAlertWithText("40", "notice", "from B")}},
				{"A", nil},
			},
			wantAlerts: map[string]string{"40/notice": "from B"},
		},
		{
			name: "empty refresh removes only that source's alerts",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "a-notice", "from A"), exportAlertWithText("40", "", "anonymous A")}},
				{"B", []ExportAlert{exportAlertWithText("40", "b-notice", "from B"), exportAlertWithText("40", "", "anonymous B")}},
				{"B", []ExportAlert{}},
			},
			wantAlerts:    map[string]string{"40/a-notice": "from A"},
			wantAnonymous: []string{"anonymous A"},
		},
		{
			name: "deletion removes another source's winner",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "from A")}},
				{"B", []ExportAlert{exportAlertWithText("40", "notice", "from B")}},
				{"A", []ExportAlert{deletedExportAlert("40", "notice")}},
			},
			wantAlerts: map[string]string{},
		},
		{
			name: "a later record recreates a deleted alert",
			refreshes: []alertRefresh{
				{"B", []ExportAlert{exportAlertWithText("40", "notice", "from B")}},
				{"A", []ExportAlert{deletedExportAlert("40", "notice")}},
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "from A again")}},
			},
			wantAlerts: map[string]string{"40/notice": "from A again"},
		},
		{
			name: "deleting an unknown alert is a no-op",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "kept", "kept"), deletedExportAlert("40", "never-seen")}},
			},
			wantAlerts: map[string]string{"40/kept": "kept"},
		},
		{
			name: "later duplicate in one feed wins",
			refreshes: []alertRefresh{
				{"A", []ExportAlert{exportAlertWithText("40", "notice", "first"), exportAlertWithText("40", "notice", "second")}},
			},
			wantAlerts: map[string]string{"40/notice": "second"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &Manager{}

			applyAlertRefreshes(manager, tt.refreshes...)

			identified, anonymous := exportAlertTexts(manager)
			assert.Equal(t, tt.wantAlerts, identified)
			assert.ElementsMatch(t, tt.wantAnonymous, anonymous)
		})
	}
}

// alertFeedServer serves one GTFS-RT alert feed per path. A nil body
// answers 500, so a test can make a feed's next fetch fail.
type alertFeedServer struct {
	mu     sync.Mutex
	bodies map[string][]byte
	server *httptest.Server
}

func newAlertFeedServer(t *testing.T) *alertFeedServer {
	t.Helper()
	feeds := &alertFeedServer{bodies: make(map[string][]byte)}
	feeds.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		feeds.mu.Lock()
		body := feeds.bodies[r.URL.Path]
		feeds.mu.Unlock()
		if body == nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(feeds.server.Close)
	return feeds
}

func (feeds *alertFeedServer) serve(t *testing.T, path string, timestamp *uint64, entities ...*gtfsrt.FeedEntity) {
	t.Helper()
	body, err := proto.Marshal(&gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: timestamp},
		Entity: entities,
	})
	require.NoError(t, err)
	feeds.mu.Lock()
	defer feeds.mu.Unlock()
	feeds.bodies[path] = body
}

func (feeds *alertFeedServer) fail(path string) {
	feeds.mu.Lock()
	defer feeds.mu.Unlock()
	feeds.bodies[path] = nil
}

func (feeds *alertFeedServer) feed(id string, agencyIDs ...string) RTFeedConfig {
	return RTFeedConfig{
		ID:               id,
		AgencyIDs:        agencyIDs,
		ServiceAlertsURL: feeds.server.URL + "/" + id,
		RefreshInterval:  3600,
		Enabled:          true,
	}
}

func agencyAlertEntity(rawID, agencyID, text string) *gtfsrt.FeedEntity {
	return &gtfsrt.FeedEntity{
		Id: proto.String(rawID),
		Alert: &gtfsrt.Alert{
			InformedEntity: []*gtfsrt.EntitySelector{{AgencyId: proto.String(agencyID)}},
			HeaderText: &gtfsrt.TranslatedString{Translation: []*gtfsrt.TranslatedString_Translation{
				{Text: proto.String(text), Language: proto.String("en")},
			}},
		},
	}
}

// newAlertExportManager loads the RABA static feed. Feeds passed in are
// fetched once at startup and set up their agency filters; tests drive
// any later refreshes themselves.
func newAlertExportManager(t *testing.T, feeds ...RTFeedConfig) *Manager {
	t.Helper()
	manager, err := InitGTFSManager(context.Background(), Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: ":memory:",
		RTFeeds:      feeds,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return manager
}

func TestExportAlertsFollowApplicationOrderNotHeaderTime(t *testing.T) {
	manager := newAlertExportManager(t)
	feeds := newAlertFeedServer(t)
	feedA, feedB := feeds.feed("a", "25"), feeds.feed("b", "25")
	feeds.serve(t, "/a", proto.Uint64(2000), agencyAlertEntity("notice", "25", "from A"))
	feeds.serve(t, "/b", proto.Uint64(1000), agencyAlertEntity("notice", "25", "from B"))

	manager.updateFeedRealtime(context.Background(), feedA)
	manager.updateFeedRealtime(context.Background(), feedB)

	identified, _ := exportAlertTexts(manager)
	assert.Equal(t, map[string]string{"25/notice": "from B"}, identified)
}

func TestExportAlertsIgnoreRefreshesWithoutHeaderTimeOrData(t *testing.T) {
	manager := newAlertExportManager(t)
	feeds := newAlertFeedServer(t)
	feed := feeds.feed("a", "25")
	feeds.serve(t, "/a", proto.Uint64(1000), agencyAlertEntity("notice", "25", "kept"))
	manager.updateFeedRealtime(context.Background(), feed)

	feeds.serve(t, "/a", nil)
	manager.updateFeedRealtime(context.Background(), feed)
	identified, _ := exportAlertTexts(manager)
	assert.Equal(t, map[string]string{"25/notice": "kept"}, identified, "feed without a header timestamp")

	feeds.fail("/a")
	manager.updateFeedRealtime(context.Background(), feed)
	identified, _ = exportAlertTexts(manager)
	assert.Equal(t, map[string]string{"25/notice": "kept"}, identified, "failed fetch")
}

func TestExportAlertsSurviveStaleFeedClearing(t *testing.T) {
	manager := newAlertExportManager(t)
	feeds := newAlertFeedServer(t)
	feed := feeds.feed("a", "25")
	feeds.serve(t, "/a", proto.Uint64(1000), agencyAlertEntity("notice", "25", "kept"))
	manager.updateFeedRealtime(context.Background(), feed)

	manager.clearFeedData("a")

	identified, _ := exportAlertTexts(manager)
	assert.Equal(t, map[string]string{"25/notice": "kept"}, identified)
}

func TestExportAlertsKeepAlertsTheAgencyFilterDrops(t *testing.T) {
	feeds := newAlertFeedServer(t)
	feed := feeds.feed("a", "25")
	feeds.serve(t, "/a", proto.Uint64(1000), agencyAlertEntity("elsewhere", "99", "other agency"))

	manager := newAlertExportManager(t, feed)

	assert.Empty(t, manager.GetAlertsByIDs("", "", "99"), "JSON view still filters by agency")
	identified, _ := exportAlertTexts(manager)
	assert.Equal(t, map[string]string{"25/elsewhere": "other agency"}, identified)
}

func TestExportAlertsLeaveOutFeedsWithoutRecordAgency(t *testing.T) {
	manager := newAlertExportManager(t)
	feeds := newAlertFeedServer(t)
	feed := feeds.feed("unscoped")
	feeds.serve(t, "/unscoped", proto.Uint64(1000),
		agencyAlertEntity("notice", "25", "identified"),
		agencyAlertEntity("", "25", "anonymous"))

	manager.updateFeedRealtime(context.Background(), feed)

	assert.Empty(t, manager.ExportAlerts())
	assert.Len(t, manager.GetAlertsByIDs("", "", "25"), 1, "JSON view is unchanged")
}

func TestExportAlertsUseFallbackRecordAgency(t *testing.T) {
	manager := newAlertExportManager(t)
	feeds := newAlertFeedServer(t)
	feed := feeds.feed("unscoped")
	feed.FallbackAlertRecordAgencyID = "40"
	feeds.serve(t, "/unscoped", proto.Uint64(1000), agencyAlertEntity("notice", "20", "cross agency"))

	manager.updateFeedRealtime(context.Background(), feed)

	identified, _ := exportAlertTexts(manager)
	assert.Equal(t, map[string]string{"40/notice": "cross agency"}, identified)
}

func TestExportAlertSelectorAgencies(t *testing.T) {
	tests := []struct {
		name         string
		selector     ExportAlertSelector
		wantAffects  []string
		wantAgencyID string
	}{
		{
			name:         "route owner",
			selector:     ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("2LINE")}, RouteOwner: "40"},
			wantAffects:  []string{"40"},
			wantAgencyID: "40",
		},
		{
			name:         "explicit agency is kept over the route owner",
			selector:     ExportAlertSelector{Entity: gtfs.AlertInformedEntity{AgencyID: proto.String("20"), RouteID: proto.String("2LINE")}, RouteOwner: "40"},
			wantAffects:  []string{"20", "40"},
			wantAgencyID: "20",
		},
		{
			name:        "stop served by two agencies affects both without an agency ID",
			selector:    ExportAlertSelector{Entity: gtfs.AlertInformedEntity{StopID: proto.String("S")}, StopOwners: []string{"A", "B"}},
			wantAffects: []string{"A", "B"},
		},
		{
			name:        "route and stop owners that disagree affect both without an agency ID",
			selector:    ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("R"), StopID: proto.String("S")}, RouteOwner: "A", StopOwners: []string{"B"}},
			wantAffects: []string{"A", "B"},
		},
		{
			name:         "agreeing owners give one agency ID",
			selector:     ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("R"), StopID: proto.String("S")}, RouteOwner: "A", TripOwner: "A", StopOwners: []string{"A"}},
			wantAffects:  []string{"A"},
			wantAgencyID: "A",
		},
		{
			name:     "unresolved selector affects nothing",
			selector: ExportAlertSelector{Entity: gtfs.AlertInformedEntity{RouteID: proto.String("unknown")}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, agencyID := range []string{"20", "40", "A", "B", "Z"} {
				assert.Equal(t, slices.Contains(tt.wantAffects, agencyID), tt.selector.AffectsAgency(agencyID), "agency %s", agencyID)
			}
			agencyID, ok := tt.selector.AgencyID()
			assert.Equal(t, tt.wantAgencyID, agencyID)
			assert.Equal(t, tt.wantAgencyID != "", ok)
		})
	}
}

func TestExportAlertsResolveSelectorOwners(t *testing.T) {
	const rabaTripID = "84f4520e-88b6-4ee6-8975-856799bc1359"
	feeds := newAlertFeedServer(t)
	feed := feeds.feed("a", "25")
	feeds.serve(t, "/a", proto.Uint64(1000), &gtfsrt.FeedEntity{
		Id: proto.String("notice"),
		Alert: &gtfsrt.Alert{InformedEntity: []*gtfsrt.EntitySelector{
			{RouteId: proto.String("151")},
			{Trip: &gtfsrt.TripDescriptor{TripId: proto.String(rabaTripID)}},
			{StopId: proto.String("1030")},
			{RouteId: proto.String("unknown-route"), StopId: proto.String("unknown-stop")},
		}},
	})

	manager := newAlertExportManager(t, feed)

	alerts := manager.ExportAlerts()
	require.Len(t, alerts, 1)
	selectors := alerts[0].Selectors
	require.Len(t, selectors, 4)
	assert.Equal(t, "25", selectors[0].RouteOwner, "route")
	assert.Equal(t, "25", selectors[1].TripOwner, "trip known only by ID")
	assert.Equal(t, []string{"25"}, selectors[2].StopOwners, "stop")
	assert.False(t, selectors[3].AffectsAgency("25"), "unknown entities")
	agencyID, _ := selectors[2].AgencyID()
	assert.Equal(t, "25", agencyID, "stop-only selector is enriched")
}

func TestExportAlertRefreshSkipsFeedWhenOwnerLookupFails(t *testing.T) {
	manager := newAlertExportManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	alertData := &gtfs.Realtime{
		CreatedAt: time.Unix(1000, 0),
		Alerts:    []gtfs.Alert{{ID: "notice", InformedEntities: []gtfs.AlertInformedEntity{{RouteID: proto.String("151")}}}},
	}

	_, apply := manager.exportAlertRefresh(ctx, RTFeedConfig{ID: "a", AgencyIDs: []string{"25"}}, alertData, nil)

	assert.False(t, apply)
}
