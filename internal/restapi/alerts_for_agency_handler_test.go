package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func alertsURL(agencyID string) string {
	return "/api/gtfs_realtime/alerts-for-agency/" + agencyID + ".pb?key=" + vehiclePositionsKey
}

func rabaAlert(alertID, header string) gtfs.Alert {
	agencyID := testdata.Raba.ID
	return gtfs.Alert{
		ID:               alertID,
		Header:           []gtfs.AlertText{{Text: header, Language: "en"}},
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agencyID}},
	}
}

// newAlertsTestAPI returns an API whose shared test manager has the given
// alerts in the unconfigured "_test" feed, cleared when the test ends.
func newAlertsTestAPI(t *testing.T, alerts ...gtfs.Alert) *RestAPI {
	t.Helper()
	api := createTestApi(t)
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	api.GtfsManager.MockResetRealTimeData()
	for _, alert := range alerts {
		api.GtfsManager.MockAddAlert("_test", alert)
	}
	return api
}

func TestAlertsForAgencyHandler(t *testing.T) {
	api := newAlertsTestAPI(t, rabaAlert("b", "Detour"), rabaAlert("a", "Closure"))

	resp, body := getRaw(t, api, alertsURL(testdata.Raba.ID), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/x-protobuf", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
	assert.Regexp(t, `^W/"[0-9a-f]{32}"$`, resp.Header.Get("ETag"))
	assert.Empty(t, resp.Header.Get("Last-Modified"))
	feed := decodeFeed(t, body)
	raba := testdata.Raba.ID
	assert.Equal(t, []string{raba + "_a", raba + "_b"}, feedEntityIDs(feed))
	assert.Equal(t, "Closure", feed.GetEntity()[0].GetAlert().GetHeaderText().GetTranslation()[0].GetText())
}

func TestAlertsForAgencyHandler_ConditionalRequests(t *testing.T) {
	api := newAlertsTestAPI(t, rabaAlert("a", "Closure"))
	first, _ := getRaw(t, api, alertsURL(testdata.Raba.ID), nil)
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)

	t.Run("matching If-None-Match gets 304", func(t *testing.T) {
		resp, body := getRaw(t, api, alertsURL(testdata.Raba.ID), http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusNotModified, resp.StatusCode)
		assert.Empty(t, body)
		assert.Equal(t, etag, resp.Header.Get("ETag"))
	})

	t.Run("a vehicle-only update keeps the ETag", func(t *testing.T) {
		api.GtfsManager.MockSetAgencyVehicles("_test", testdata.Raba.ID, []gtfs.Vehicle{positionedVehicle("V1", 47.1)})
		resp, _ := getRaw(t, api, alertsURL(testdata.Raba.ID), nil)
		assert.Equal(t, etag, resp.Header.Get("ETag"))
	})

	t.Run("a new alert gives a new ETag", func(t *testing.T) {
		api.GtfsManager.MockAddAlert("_test", rabaAlert("b", "Detour"))
		resp, _ := getRaw(t, api, alertsURL(testdata.Raba.ID), http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEqual(t, etag, resp.Header.Get("ETag"))
	})
}

func TestAlertsForAgencyHandler_UnknownAgency(t *testing.T) {
	api := newAlertsTestAPI(t, rabaAlert("a", "Closure"))

	resp, body := getRaw(t, api, alertsURL("nope"), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	feed := decodeFeed(t, body)
	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.NotZero(t, feed.GetHeader().GetTimestamp())
	assert.Empty(t, feed.GetEntity())
}
