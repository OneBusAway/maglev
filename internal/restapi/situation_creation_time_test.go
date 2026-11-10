package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
)

// TestSituationCreationTimeUsesIngestion exercises a real protobuf fetch before
// checking the public endpoint. Request time and feed-header time deliberately
// differ from the bounded ingestion interval.
func TestSituationCreationTimeUsesIngestion(t *testing.T) {
	payload, err := proto.Marshal(&gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0"), Timestamp: proto.Uint64(1)},
		Entity: []*gtfsrt.FeedEntity{{Id: proto.String("creation-test"), Alert: &gtfsrt.Alert{
			InformedEntity: []*gtfsrt.EntitySelector{{AgencyId: proto.String("25")}},
		}}},
	})
	require.NoError(t, err)
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(feed.Close)
	config := gtfs.Config{GtfsURL: models.GetFixturePath(t, "raba.zip"), GTFSDataPath: ":memory:", RTFeeds: []gtfs.RTFeedConfig{{
		ID: "creation-feed", ServiceAlertsURL: feed.URL, RefreshInterval: 86400, Enabled: true,
	}}}
	before := time.Now().UnixMilli()
	manager, err := gtfs.InitGTFSManager(context.Background(), config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	requestClock := clock.NewMockClock(time.Now().Add(24 * time.Hour))
	application := &app.Application{GtfsManager: manager, GtfsConfig: config,
		Config: appconf.Config{Env: appconf.EnvFlagToEnvironment("test"), ApiKeys: []string{"TEST"}, RateLimit: 100},
		Clock:  requestClock,
	}
	api := NewRestAPI(application)
	t.Cleanup(api.Shutdown)
	after := time.Now().UnixMilli()
	resp, result := callAPIHandler[SituationResponse](t, api, "/api/where/situation/25_creation-test.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "25_creation-test", result.Data.Entry.ID)
	require.GreaterOrEqual(t, result.Data.Entry.CreationTime.UnixMilli(), before)
	require.LessOrEqual(t, result.Data.Entry.CreationTime.UnixMilli(), after)
	createdAt := result.Data.Entry.CreationTime
	requestClock.Advance(24 * time.Hour)
	resp, repeated := callAPIHandler[SituationResponse](t, api, "/api/where/situation/25_creation-test.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, createdAt, repeated.Data.Entry.CreationTime, "reading must not restamp the alert")
	resp, arrivals := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
		"/api/where/arrivals-and-departures-for-stop/25_1056.json?key=TEST&minutesBefore=0&minutesAfter=0")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var found bool
	for _, situation := range arrivals.Data.References.Situations {
		if situation.ID == "25_creation-test" {
			found = true
			require.Equal(t, createdAt, situation.CreationTime, "entry and reference must carry the same ingestion time")
		}
	}
	require.True(t, found, "agency-wide alert must remain in empty-window references")
}
