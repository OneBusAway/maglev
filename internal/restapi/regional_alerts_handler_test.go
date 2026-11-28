package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

const regionalAlertsURL = "/api/gtfs_realtime/alerts.pb?key=" + vehiclePositionsKey

func TestRegionalAlertsHandler(t *testing.T) {
	raba := testdata.Raba.ID
	otherAgency := "OTHER"
	api := newAlertsTestAPI(t,
		rabaAlert("b", "Detour"),
		rabaAlert("a", "Closure"),
		gtfs.Alert{
			ID:               "x",
			Header:           []gtfs.AlertText{{Text: "Systemwide Alert", Language: "en"}},
			InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &otherAgency}},
		},
		gtfs.Alert{
			ID:     "c_agencyless",
			Header: []gtfs.AlertText{{Text: "Agencyless Alert", Language: "en"}},
		},
	)

	resp, body := getRaw(t, api, regionalAlertsURL, nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/x-protobuf", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
	assert.Regexp(t, `^W/"[0-9a-f]{32}"$`, resp.Header.Get("ETag"))
	assert.Empty(t, resp.Header.Get("Last-Modified"))

	feed := decodeFeed(t, body)
	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())

	// Sorted in ascending order: "25_a", "25_b", "OTHER_x", "c_agencyless"
	expectedIDs := []string{raba + "_a", raba + "_b", "OTHER_x", "c_agencyless"}
	assert.Equal(t, expectedIDs, feedEntityIDs(feed))

	// Verify text content
	for _, entity := range feed.GetEntity() {
		switch entity.GetId() {
		case "OTHER_x":
			assert.Equal(t, "Systemwide Alert", entity.GetAlert().GetHeaderText().GetTranslation()[0].GetText())
		case "c_agencyless":
			assert.Equal(t, "Agencyless Alert", entity.GetAlert().GetHeaderText().GetTranslation()[0].GetText())
		case raba + "_a":
			assert.Equal(t, "Closure", entity.GetAlert().GetHeaderText().GetTranslation()[0].GetText())
		}
	}
}

func TestRegionalAlertsHandler_ConditionalRequests(t *testing.T) {
	api := newAlertsTestAPI(t, rabaAlert("a", "Closure"))
	first, _ := getRaw(t, api, regionalAlertsURL, nil)
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)

	t.Run("matching If-None-Match gets 304", func(t *testing.T) {
		resp, body := getRaw(t, api, regionalAlertsURL, http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusNotModified, resp.StatusCode)
		assert.Empty(t, body)
		assert.Equal(t, etag, resp.Header.Get("ETag"))
	})

	t.Run("a vehicle-only update keeps the ETag", func(t *testing.T) {
		api.GtfsManager.MockSetAgencyVehicles("_test", testdata.Raba.ID, []gtfs.Vehicle{positionedVehicle("V1", 47.1)})
		resp, _ := getRaw(t, api, regionalAlertsURL, nil)
		assert.Equal(t, etag, resp.Header.Get("ETag"))
	})

	t.Run("a new alert gives a new ETag", func(t *testing.T) {
		api.GtfsManager.MockAddAlert("_test", rabaAlert("b", "Detour"))
		resp, _ := getRaw(t, api, regionalAlertsURL, http.Header{"If-None-Match": {etag}})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEqual(t, etag, resp.Header.Get("ETag"))
	})
}

func TestRegionalAlertsHandler_RequireKey(t *testing.T) {
	api := newAlertsTestAPI(t, rabaAlert("a", "Closure"))
	resp, _ := getRaw(t, api, "/api/gtfs_realtime/alerts.pb", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRegionalAlertsHandler_QueryParameters(t *testing.T) {
	api := newAlertsTestAPI(t, rabaAlert("a", "Closure"))
	_, plain := getRaw(t, api, regionalAlertsURL, nil)

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"epoch milliseconds time", "&time=1792287624289", http.StatusOK},
		{"legacy time format", "&time=2026-10-18_10-00-00", http.StatusOK},
		{"unparseable time", "&time=abc", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := getRaw(t, api, regionalAlertsURL+tt.query, nil)
			require.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantStatus == http.StatusOK {
				assert.Equal(t, plain, body, "the parameter must not change the feed")
			}
		})
	}
}

func TestRegionalAlertsHandler_EmptyFeed(t *testing.T) {
	api := newAlertsTestAPI(t)

	resp, body := getRaw(t, api, regionalAlertsURL, nil)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	feed := decodeFeed(t, body)
	assert.Equal(t, "2.0", feed.GetHeader().GetGtfsRealtimeVersion())
	assert.NotZero(t, feed.GetHeader().GetTimestamp())
	assert.Empty(t, feed.GetEntity())
}
