package restapi

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func tripUpdatesURL(agencyID, key string) string {
	return "/api/gtfs_realtime/trip-updates-for-agency/" + agencyID + ".pb?key=" + key
}

func predictedTrip(tripID, stopID string) gtfs.Trip {
	arrival := time.Now().Add(5 * time.Minute)
	return gtfs.Trip{
		ID: gtfs.TripID{ID: tripID},
		StopTimeUpdates: []gtfs.StopTimeUpdate{
			{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Time: &arrival}},
		},
	}
}

// newTripUpdatesTestAPI returns an API whose RABA agency has the given trip
// updates in the shared test manager, cleared when the test ends.
func newTripUpdatesTestAPI(t *testing.T, trips ...gtfs.Trip) *RestAPI {
	t.Helper()
	api := createTestApi(t)
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	api.GtfsManager.MockResetRealTimeData()
	api.GtfsManager.MockSetAgencyTripUpdates("_test", testdata.Raba.ID, trips)
	return api
}

func TestTripUpdatesForAgencyHandler(t *testing.T) {
	api := newTripUpdatesTestAPI(t, predictedTrip("T2", "S2"), predictedTrip("T1", "S1"))

	resp, body := getRaw(t, api, tripUpdatesURL(testdata.Raba.ID, vehiclePositionsKey), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/x-protobuf", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
	assert.Regexp(t, `^W/"[0-9a-f]{32}"$`, resp.Header.Get("ETag"))
	assert.Empty(t, resp.Header.Get("Last-Modified"))
	feed := decodeFeed(t, body)
	assert.Equal(t, []string{"T1", "T2"}, feedEntityIDs(feed))
	assert.Equal(t, "S1", feed.GetEntity()[0].GetTripUpdate().GetStopTimeUpdate()[0].GetStopId())
}

func TestTripUpdatesForAgencyHandler_DistinctTripIdentities(t *testing.T) {
	type tripFields struct {
		tripID, date, startTime string
		stopSequence            uint32
	}
	trips := []struct {
		entityID string
		fields   tripFields
	}{
		{"T1_20261015", tripFields{"T1", "20261015", "", 1}},
		{"T1%5F20261015", tripFields{"T1_20261015", "", "", 2}},
		{"T2_08:00:00", tripFields{"T2", "", "08:00:00", 3}},
		{"T2%5F08:00:00", tripFields{"T2_08:00:00", "", "", 4}},
		{"T1%255F20261015", tripFields{"T1%5F20261015", "", "", 5}},
	}
	want := make(map[string]tripFields, len(trips))
	entities := make([]*gtfsrt.FeedEntity, 0, len(trips))
	for _, trip := range trips {
		fields := trip.fields
		want[trip.entityID] = fields
		descriptor := &gtfsrt.TripDescriptor{TripId: proto.String(fields.tripID), RouteId: proto.String("151")}
		if fields.date != "" {
			descriptor.StartDate = proto.String(fields.date)
		}
		if fields.startTime != "" {
			descriptor.StartTime = proto.String(fields.startTime)
		}
		entities = append(entities, &gtfsrt.FeedEntity{
			Id: proto.String(strconv.FormatUint(uint64(fields.stopSequence), 10)),
			TripUpdate: &gtfsrt.TripUpdate{
				Trip: descriptor,
				StopTimeUpdate: []*gtfsrt.TripUpdate_StopTimeUpdate{{
					StopSequence: proto.Uint32(fields.stopSequence),
					Departure:    &gtfsrt.TripUpdate_StopTimeEvent{Delay: proto.Int32(60)},
				}},
			},
		})
	}
	reversed := slices.Clone(entities)
	slices.Reverse(reversed)
	for _, order := range []struct {
		name     string
		entities []*gtfsrt.FeedEntity
	}{
		{"original order", entities},
		{"reversed order", reversed},
	} {
		t.Run(order.name, func(t *testing.T) {
			payload, err := proto.Marshal(&gtfsrt.FeedMessage{
				Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")},
				Entity: order.entities,
			})
			require.NoError(t, err)
			api, cleanup := createTestApiWithTripUpdates(t, clock.RealClock{}, payload)
			defer cleanup()
			ingested := 0
			for _, trip := range api.GtfsManager.GetRealTimeTrips() {
				if trip.IsEntityInMessage {
					ingested++
				}
			}
			require.Equal(t, len(trips), ingested, "all distinct trips must survive ingestion")

			resp, body := getRaw(t, api, tripUpdatesURL(testdata.Raba.ID, "TEST"), nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			feed := decodeFeed(t, body)
			require.Len(t, feed.GetEntity(), len(want))
			got := make(map[string]tripFields, len(want))
			for _, entity := range feed.GetEntity() {
				update := entity.GetTripUpdate()
				trip := update.GetTrip()
				require.NotContains(t, got, entity.GetId())
				require.Len(t, update.GetStopTimeUpdate(), 1)
				got[entity.GetId()] = tripFields{
					trip.GetTripId(), trip.GetStartDate(), trip.GetStartTime(), update.GetStopTimeUpdate()[0].GetStopSequence(),
				}
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestTripUpdatesForAgencyHandler_ConditionalRequests(t *testing.T) {
	api := newTripUpdatesTestAPI(t, predictedTrip("T1", "S1"))
	first, _ := getRaw(t, api, tripUpdatesURL(testdata.Raba.ID, vehiclePositionsKey), nil)
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)

	t.Run("matching If-None-Match gets 304", func(t *testing.T) {
		resp, body := getRaw(t, api, tripUpdatesURL(testdata.Raba.ID, vehiclePositionsKey), http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusNotModified, resp.StatusCode)
		assert.Empty(t, body)
		assert.Equal(t, etag, resp.Header.Get("ETag"))
	})

	t.Run("new trip updates give a new ETag", func(t *testing.T) {
		api.GtfsManager.MockSetAgencyTripUpdates("_test", testdata.Raba.ID, []gtfs.Trip{predictedTrip("T1", "S9")})
		resp, _ := getRaw(t, api, tripUpdatesURL(testdata.Raba.ID, vehiclePositionsKey), http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEqual(t, etag, resp.Header.Get("ETag"))
	})
}

func TestTripUpdatesForAgencyHandler_UnknownAgency(t *testing.T) {
	api := newTripUpdatesTestAPI(t, predictedTrip("T1", "S1"))

	resp, body := getRaw(t, api, tripUpdatesURL("nope", vehiclePositionsKey), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	feed := decodeFeed(t, body)
	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.NotZero(t, feed.GetHeader().GetTimestamp())
	assert.Empty(t, feed.GetEntity())
}

// TestTripUpdatesForAgencyHandler_RabaFeed runs RABA's recorded trip
// updates through ingestion, so attribution happens as in production.
func TestTripUpdatesForAgencyHandler_RabaFeed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
	require.NoError(t, err)
	upstream, err := gtfs.ParseRealtime(data, &gtfs.ParseRealtimeOptions{})
	require.NoError(t, err)
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	resp, body := getRaw(t, api, tripUpdatesURL(testdata.Raba.ID, "TEST"), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	wantIDs := map[string]bool{}
	for _, trip := range upstream.Trips {
		if trip.IsEntityInMessage && trip.ID.ID != "" {
			wantIDs[trip.ID.ID] = true
		}
	}
	require.NotEmpty(t, wantIDs)
	feed := decodeFeed(t, body)
	gotIDs := map[string]bool{}
	for _, entity := range feed.GetEntity() {
		gotIDs[entity.GetTripUpdate().GetTrip().GetTripId()] = true
	}
	assert.Equal(t, wantIDs, gotIDs)
}
