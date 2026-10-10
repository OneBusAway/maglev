package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func TestNewGtfsRealtimeFeedHeader(t *testing.T) {
	feed := newGtfsRealtimeFeed(time.UnixMilli(1791581968999))
	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.Equal(t, uint64(1791581968), feed.GetHeader().GetTimestamp())
	assert.Empty(t, feed.GetEntity())

	zero := newGtfsRealtimeFeed(time.Unix(0, 0))
	require.NotNil(t, zero.GetHeader().Timestamp, "epoch zero is a supplied timestamp, not an absent one")
	assert.Equal(t, uint64(0), zero.GetHeader().GetTimestamp())
}

func TestWriteGtfsRealtimeFeedFormatsAreEquivalent(t *testing.T) {
	api := createTestApi(t)
	feed := newGtfsRealtimeFeed(time.Unix(1791581968, 0))
	feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
		Id: proto.String("1"),
		Vehicle: &gtfsrt.VehiclePosition{
			Vehicle:  &gtfsrt.VehicleDescriptor{Id: proto.String("bus_A")},
			Position: &gtfsrt.Position{Latitude: proto.Float32(40.5), Longitude: proto.Float32(-122.4)},
		},
	})

	binary := httptest.NewRecorder()
	api.writeGtfsRealtimeFeed(binary, httptest.NewRequest(http.MethodGet, "/", nil), gtfsRealtimeBinary, feed)
	assert.Equal(t, http.StatusOK, binary.Code)
	assert.Equal(t, "application/x-google-protobuf", binary.Header().Get("Content-Type"))
	var fromBinary gtfsrt.FeedMessage
	require.NoError(t, proto.Unmarshal(binary.Body.Bytes(), &fromBinary))

	text := httptest.NewRecorder()
	api.writeGtfsRealtimeFeed(text, httptest.NewRequest(http.MethodGet, "/", nil), gtfsRealtimeText, feed)
	assert.Equal(t, "text/plain; charset=utf-8", text.Header().Get("Content-Type"))
	var fromText gtfsrt.FeedMessage
	require.NoError(t, prototext.Unmarshal(text.Body.Bytes(), &fromText))

	assert.True(t, proto.Equal(&fromBinary, &fromText))
	assert.True(t, proto.Equal(feed, &fromBinary))
}

func TestWriteGtfsRealtimeFeedReportsMarshalFailure(t *testing.T) {
	api := createTestApi(t)
	feed := newGtfsRealtimeFeed(time.Unix(1, 0))
	// Position.latitude is a proto2 required field, so this feed cannot marshal.
	feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
		Id:      proto.String("1"),
		Vehicle: &gtfsrt.VehiclePosition{Position: &gtfsrt.Position{Longitude: proto.Float32(1)}},
	})
	recorder := httptest.NewRecorder()
	api.writeGtfsRealtimeFeed(recorder, httptest.NewRequest(http.MethodGet, "/", nil), gtfsRealtimeBinary, feed)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}

func TestGtfsRealtimeCacheMiddlewareCoversVersionRejection(t *testing.T) {
	api := createTestApi(t)
	server := httptest.NewServer(api.SetupAPIRoutes())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/gtfs_realtime/vehicle-positions-for-agency/25.pb?key=TEST&version=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Cache-Control"), "no-store")

	other, err := http.Get(server.URL + "/api/where/current-time.json?key=TEST&version=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Body.Close() })
	assert.Empty(t, other.Header.Get("Cache-Control"), "non-export routes keep their existing headers")
}

func TestExportPayloadID(t *testing.T) {
	assert.Equal(t, "trip_with_underscores", exportPayloadID("trip_with_underscores", "40", true))
	assert.Equal(t, "40_trip_with_underscores", exportPayloadID("trip_with_underscores", "40", false))
	assert.Equal(t, "trip", exportPayloadID("trip", "", false), "unknown owner is not invented")
}

func TestFormatGtfsServiceTime(t *testing.T) {
	assert.Equal(t, "08:05:09", formatGtfsServiceTime(8*time.Hour+5*time.Minute+9*time.Second))
	assert.Equal(t, "25:00:00", formatGtfsServiceTime(25*time.Hour))
}
