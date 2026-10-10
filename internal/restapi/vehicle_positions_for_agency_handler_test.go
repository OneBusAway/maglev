package restapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

const vehiclePositionsPath = "/api/gtfs_realtime/vehicle-positions-for-agency/"

// exemptExportKey is exempt from the test API's 5-request rate limit, so a
// table of requests against one API instance is not throttled. It is also
// the key the iOS app sends.
const exemptExportKey = "org.onebusaway.iphone"

// callGtfsRealtimeEndpoint requests an export through the full middleware
// chain and returns the raw body, which the JSON helpers cannot decode.
func callGtfsRealtimeEndpoint(t *testing.T, api *RestAPI, endpoint string) (*http.Response, []byte) {
	t.Helper()
	server := httptest.NewServer(api.SetupAPIRoutes())
	t.Cleanup(server.Close)
	resp, err := http.Get(server.URL + endpoint)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, body
}

func decodeGtfsRealtimeBody(t *testing.T, resp *http.Response, body []byte) *gtfsrt.FeedMessage {
	t.Helper()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var feed gtfsrt.FeedMessage
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		require.NoError(t, prototext.Unmarshal(body, &feed))
	} else {
		require.NoError(t, proto.Unmarshal(body, &feed))
	}
	return &feed
}

func newVehicleExportTestApi(t *testing.T) *RestAPI {
	t.Helper()
	api := createTestApiWithClock(t, clock.NewMockClock(vehicleExportNow))
	updated := vehicleExportNow.Add(-time.Minute)
	api.GtfsManager.MockSetExportVehicles([]internalgtfs.ExportVehicle{
		{
			Vehicle: gtfs.Vehicle{
				ID:        &gtfs.VehicleID{ID: "5701"},
				Timestamp: &updated,
				Position:  &gtfs.Position{Latitude: proto.Float32(40.5), Longitude: proto.Float32(-122.4)},
			},
			ActiveTripID:  "trip_on_24",
			ActiveRouteID: "24",
			Block:         &internalgtfs.BlockMatch{TripID: "trip_on_24", RouteID: "24", AgencyID: "25"},
		},
		{
			Vehicle: gtfs.Vehicle{
				ID:        &gtfs.VehicleID{ID: "25_tripless"},
				Timestamp: &updated,
				Position:  &gtfs.Position{Latitude: proto.Float32(40.6), Longitude: proto.Float32(-122.3)},
			},
		},
	})
	t.Cleanup(func() { api.GtfsManager.MockSetExportVehicles(nil) })
	return api
}

func TestVehiclePositionsForAgencyFormatsMatch(t *testing.T) {
	api := newVehicleExportTestApi(t)

	binaryResp, binaryBody := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+"25.pb?key="+exemptExportKey)
	assert.Equal(t, "application/x-google-protobuf", binaryResp.Header.Get("Content-Type"))
	assert.Contains(t, binaryResp.Header.Get("Cache-Control"), "no-store")
	fromBinary := decodeGtfsRealtimeBody(t, binaryResp, binaryBody)

	textResp, textBody := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+"25.pbtext?key="+exemptExportKey)
	assert.Equal(t, "text/plain; charset=utf-8", textResp.Header.Get("Content-Type"))
	fromText := decodeGtfsRealtimeBody(t, textResp, textBody)

	assert.True(t, proto.Equal(fromBinary, fromText))
	assert.Equal(t, "2.0", fromBinary.GetHeader().GetGtfsRealtimeVersion())
	assert.Equal(t, uint64(vehicleExportNow.Unix()), fromBinary.GetHeader().GetTimestamp())
	require.Len(t, fromBinary.Entity, 2)
	// Candidates sort by raw vehicle ID, so "25_tripless" comes before "5701".
	assert.Equal(t, "tripless", fromBinary.Entity[0].GetVehicle().GetVehicle().GetId())
	assert.Equal(t, "5701", fromBinary.Entity[1].GetVehicle().GetVehicle().GetId())
}

func TestVehiclePositionsForAgencyParameters(t *testing.T) {
	api := newVehicleExportTestApi(t)
	tests := []struct {
		name         string
		query        string
		pathID       string
		wantStatus   int
		wantEntities int
		wantVehicle  string
	}{
		{"route filter", "&routeFilterId=24", "25.pb", http.StatusOK, 1, "5701"},
		{"qualified route filter does not match", "&routeFilterId=25_24", "25.pb", http.StatusOK, 0, ""},
		{"keep agency prefixes", "&removeAgencyIds=false&routeFilterId=24", "25.pb", http.StatusOK, 1, "25_5701"},
		{"unknown agency is an empty feed", "", "unknown.pb", http.StatusOK, 0, ""},
		{"stale request time", "&time=" + strconv.FormatInt(vehicleExportNow.Add(20*time.Minute).UnixMilli(), 10), "25.pb", http.StatusOK, 0, ""},
		{"supported version", "&version=2", "25.pb", http.StatusOK, 2, ""},
		{"unsupported version", "&version=1", "25.pb", http.StatusBadRequest, 0, ""},
		{"malformed time", "&time=bad-time", "25.pb", http.StatusBadRequest, 0, ""},
		{"negative epoch", "&time=-1000", "25.pb", http.StatusBadRequest, 0, ""},
		{"invalid boolean", "&removeAgencyIds=maybe", "25.pb", http.StatusBadRequest, 0, ""},
		{"missing format suffix", "", "25", http.StatusNotFound, 0, ""},
		{"json suffix", "", "25.json", http.StatusNotFound, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+tt.pathID+"?key="+exemptExportKey+tt.query)
			assert.Contains(t, resp.Header.Get("Cache-Control"), "no-store")
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, tt.wantStatus, resp.StatusCode, string(body))
				return
			}
			feed := decodeGtfsRealtimeBody(t, resp, body)
			require.Len(t, feed.Entity, tt.wantEntities)
			if tt.wantVehicle != "" {
				assert.Equal(t, tt.wantVehicle, feed.Entity[0].GetVehicle().GetVehicle().GetId())
			}
		})
	}
}

func TestVehiclePositionsForAgencyEpochZero(t *testing.T) {
	api := newVehicleExportTestApi(t)
	resp, body := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+"25.pb?key="+exemptExportKey+"&time=0")
	feed := decodeGtfsRealtimeBody(t, resp, body)
	require.NotNil(t, feed.GetHeader().Timestamp)
	assert.Equal(t, uint64(0), feed.GetHeader().GetTimestamp())
}

func TestVehiclePositionsForAgencyAuthorization(t *testing.T) {
	api := newVehicleExportTestApi(t)
	for _, pathID := range []string{"25.pb", "25.pbtext"} {
		ok, _ := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+pathID+"?key=TEST")
		require.Equal(t, http.StatusOK, ok.StatusCode)

		missing, _ := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+pathID)
		assert.Equal(t, http.StatusUnauthorized, missing.StatusCode, "a prior caller's feed is not reused")

		invalid, _ := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+pathID+"?key=wrong")
		assert.Equal(t, http.StatusUnauthorized, invalid.StatusCode)
	}
}

func TestVehiclePositionsForAgencyRateLimit(t *testing.T) {
	api := newVehicleExportTestApi(t)
	server := httptest.NewServer(api.SetupAPIRoutes())
	t.Cleanup(server.Close)

	sawTooMany := false
	for range 20 {
		resp, err := http.Get(server.URL + vehiclePositionsPath + "25.pb?key=test-rate-limit")
		require.NoError(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			sawTooMany = true
			break
		}
	}
	assert.True(t, sawTooMany)
}

func TestVehiclePositionsForAgencyIgnoresConditionalHeaders(t *testing.T) {
	api := newVehicleExportTestApi(t)
	server := httptest.NewServer(api.SetupAPIRoutes())
	t.Cleanup(server.Close)
	req, err := http.NewRequest(http.MethodGet, server.URL+vehiclePositionsPath+"25.pb?key="+exemptExportKey, nil)
	require.NoError(t, err)
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("If-Modified-Since", time.Now().UTC().Format(http.TimeFormat))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Cache-Control"), "no-store")
}

func TestVehiclePositionsForAgencyLooksUpRouteOwners(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(vehicleExportNow))
	updated := vehicleExportNow.Add(-time.Minute)
	api.GtfsManager.MockSetExportVehicles([]internalgtfs.ExportVehicle{{
		Vehicle: gtfs.Vehicle{
			ID:        &gtfs.VehicleID{ID: "25_unmatched"},
			Timestamp: &updated,
			Position:  &gtfs.Position{Latitude: proto.Float32(40.5), Longitude: proto.Float32(-122.4)},
		},
		ActiveTripID:  "trip_without_block",
		ActiveRouteID: "24",
	}})
	t.Cleanup(func() { api.GtfsManager.MockSetExportVehicles(nil) })

	resp, body := callGtfsRealtimeEndpoint(t, api, vehiclePositionsPath+"25.pb?key="+exemptExportKey+"&removeAgencyIds=false")
	feed := decodeGtfsRealtimeBody(t, resp, body)
	require.Len(t, feed.Entity, 1)
	trip := feed.Entity[0].GetVehicle().GetTrip()
	assert.Equal(t, "25_24", trip.GetRouteId())
	assert.Equal(t, "25_trip_without_block", trip.GetTripId())
}
