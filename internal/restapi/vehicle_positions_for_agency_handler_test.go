package restapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

// vehiclePositionsKey is exempt from rate limiting, so a test can make many
// requests against one API instance.
const vehiclePositionsKey = "org.onebusaway.iphone"

func vehiclePositionsURL(agencyID string) string {
	return "/api/gtfs_realtime/vehicle-positions-for-agency/" + agencyID + ".pb?key=" + vehiclePositionsKey
}

func positionedVehicle(vehicleID string, latitude float32) gtfs.Vehicle {
	longitude := float32(-122.3)
	now := time.Now()
	return gtfs.Vehicle{
		ID:        &gtfs.VehicleID{ID: vehicleID},
		Position:  &gtfs.Position{Latitude: &latitude, Longitude: &longitude},
		Timestamp: &now,
	}
}

// newVehiclePositionsTestAPI returns an API whose RABA agency has the given
// vehicles in the shared test manager, cleared when the test ends.
func newVehiclePositionsTestAPI(t *testing.T, vehicles ...gtfs.Vehicle) *RestAPI {
	t.Helper()
	api := createTestApi(t)
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	api.GtfsManager.MockSetAgencyVehicles("_test", testdata.Raba.ID, vehicles)
	return api
}

// getRaw requests path with the given headers and returns the response and
// its (decompressed) body.
func getRaw(t *testing.T, api *RestAPI, path string, header http.Header) (*http.Response, []byte) {
	t.Helper()
	server := httptest.NewServer(api.SetupAPIRoutes())
	t.Cleanup(server.Close)
	req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	require.NoError(t, err)
	for name, values := range header {
		req.Header[name] = values
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, body
}

func decodeFeed(t *testing.T, body []byte) *gtfsrt.FeedMessage {
	t.Helper()
	feed := &gtfsrt.FeedMessage{}
	require.NoError(t, proto.Unmarshal(body, feed))
	return feed
}

func feedEntityIDs(feed *gtfsrt.FeedMessage) []string {
	var ids []string
	for _, entity := range feed.GetEntity() {
		ids = append(ids, entity.GetId())
	}
	return ids
}

func TestVehiclePositionsForAgencyHandler(t *testing.T) {
	api := newVehiclePositionsTestAPI(t, positionedVehicle("V2", 47.2), positionedVehicle("V1", 47.1))

	resp, body := getRaw(t, api, vehiclePositionsURL(testdata.Raba.ID), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/x-protobuf", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
	assert.Regexp(t, `^W/"[0-9a-f]{32}"$`, resp.Header.Get("ETag"))
	assert.Empty(t, resp.Header.Get("Last-Modified"))
	feed := decodeFeed(t, body)
	assert.Equal(t, []string{"V1", "V2"}, feedEntityIDs(feed))
	assert.InDelta(t, 47.1, feed.GetEntity()[0].GetVehicle().GetPosition().GetLatitude(), 0.001)
}

func TestVehiclePositionsForAgencyHandler_ConditionalRequests(t *testing.T) {
	api := newVehiclePositionsTestAPI(t, positionedVehicle("V1", 47.1))
	first, _ := getRaw(t, api, vehiclePositionsURL(testdata.Raba.ID), nil)
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)

	t.Run("same snapshot, same ETag", func(t *testing.T) {
		again, _ := getRaw(t, api, vehiclePositionsURL(testdata.Raba.ID), nil)
		assert.Equal(t, etag, again.Header.Get("ETag"))
	})

	t.Run("matching If-None-Match gets 304", func(t *testing.T) {
		resp, body := getRaw(t, api, vehiclePositionsURL(testdata.Raba.ID), http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusNotModified, resp.StatusCode)
		assert.Empty(t, body)
		assert.Equal(t, etag, resp.Header.Get("ETag"))
		assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
	})

	t.Run("new data, new ETag", func(t *testing.T) {
		api.GtfsManager.MockSetAgencyVehicles("_test", testdata.Raba.ID, []gtfs.Vehicle{positionedVehicle("V1", 47.9)})
		resp, _ := getRaw(t, api, vehiclePositionsURL(testdata.Raba.ID), http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEqual(t, etag, resp.Header.Get("ETag"))
	})
}

func TestVehiclePositionsForAgencyHandler_UnknownAgency(t *testing.T) {
	api := newVehiclePositionsTestAPI(t, positionedVehicle("V1", 47.1))

	resp, body := getRaw(t, api, vehiclePositionsURL("nope"), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	feed := decodeFeed(t, body)
	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.Empty(t, feed.GetEntity())
}

func TestVehiclePositionsForAgencyHandler_ResetClearsAttribution(t *testing.T) {
	api := newVehiclePositionsTestAPI(t, positionedVehicle("V1", 47.1))
	api.GtfsManager.MockResetRealTimeData()

	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{positionedVehicle("V1", 47.1)})

	assert.Empty(t, api.GtfsManager.VehiclePositionsForAgency(testdata.Raba.ID).Vehicles, "an unattributed test vehicle must not inherit an earlier test's agency")
}
