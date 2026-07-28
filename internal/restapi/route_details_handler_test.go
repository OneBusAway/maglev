package restapi

import (
	"net/http"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

type routeDetailsResponse struct {
	models.ResponseModel
	Data struct {
		LimitExceeded bool                       `json:"limitExceeded"`
		List          []models.RouteDetailsEntry `json:"list"`
		References    models.ReferencesModel     `json:"references"`
	} `json:"data"`
}

func routeDetailsURL(routeID string) string {
	return "/api/where/route-details/" + routeID + ".json?key=TEST"
}

func routeDetailsURLWithTime(routeID, timeStr string) string {
	return "/api/where/route-details/" + routeID + ".json?key=TEST&time=" + timeStr
}

func TestRouteDetailsHandler_Success(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Len(t, model.Data.List, 1)
	entry := model.Data.List[0]
	assert.Equal(t, testdata.Raba.ID, entry.RouteID.AgencyID)
	assert.Equal(t, "151", entry.RouteID.ID)
	assert.NotNil(t, entry.StopGroupings)
	assert.Len(t, entry.StopGroupings, 2)
	assert.Equal(t, "heuristic", entry.StopGroupings[0].Type)
	assert.Equal(t, "direction", entry.StopGroupings[1].Type)

	hasPolylines := false
	for _, grouping := range entry.StopGroupings {
		for _, stopGroup := range grouping.StopGroups {
			if len(stopGroup.Polylines) > 0 {
				hasPolylines = true
				break
			}
		}
	}
	assert.True(t, hasPolylines, "expected at least one stop group to have polylines")

	assert.NotEmpty(t, model.Data.References.Agencies)
	assert.NotEmpty(t, model.Data.References.Routes)
}

func TestRouteDetailsHandler_NotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL("1_999999"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

func TestRouteDetailsHandler_InvalidTime(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURLWithTime(testdata.Route1.ID, "invalid_time"))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRouteDetailsHandler_InvalidRouteIDFormat(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL("garbage"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

func TestRouteDetailsHandler_ServiceDateParam(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[routeDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json?key=TEST&serviceDate=2024-01-01")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
}

func TestRouteDetailsHandler_NoActiveServiceForDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// pick a date far outside the test GTFS feed's service period
	resp, model := callAPIHandler[routeDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json?key=TEST&time=1999-01-01")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 1)
	entry := model.Data.List[0]
	assert.Equal(t, "151", entry.RouteID.ID)
	assert.NotEmpty(t, entry.StopGroupings)

	// route should still appear in references even with no active trips
	found := false
	for _, r := range model.Data.References.Routes {
		if r.ID == testdata.Route1.ID {
			found = true
		}
	}
	assert.True(t, found)
}

func TestRouteDetailsHandler_NoActiveTrips(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// A date far outside the test GTFS feed's service calendar
	resp, model := callAPIHandler[routeDetailsResponse](t, api,
		routeDetailsURLWithTime(testdata.Route1.ID, "946684800000")) // 2000-01-01 in ms

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 1)
	entry := model.Data.List[0]
	assert.Equal(t, "151", entry.RouteID.ID)
	assert.NotNil(t, entry.StopGroupings)
	assert.NotEmpty(t, entry.StopGroupings)
}

func TestRouteDetailsHandler_RequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// No key at all
	resp, model := callAPIHandler[routeDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)

	// Invalid/unknown key
	resp2, model2 := callAPIHandler[routeDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json?key=WRONGKEY")
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model2.Code)
}

func TestRouteDetailsHandler_SetsCacheHeaders(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	etag := resp.Header.Get("ETag")
	assert.NotEmpty(t, etag)
	assert.Equal(t, "public, max-age=300", resp.Header.Get("Cache-Control"))

	// Reissue request with ETag
	resp2, _ := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID), map[string]string{
		"If-None-Match": etag,
	})

	assert.Equal(t, http.StatusNotModified, resp2.StatusCode)
}

func TestRouteDetailsHandler_IncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[routeDetailsResponse](t, api,
		routeDetailsURL(testdata.Route1.ID)+"&includeReferences=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Empty(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Situations)

	// list data should still be fully populated even without references
	assert.Len(t, model.Data.List, 1)
	assert.NotEmpty(t, model.Data.List[0].StopGroupings)
}

func TestRouteDetailsHandler_ReferencesOrderIsDeterministic(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, model1 := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID))
	_, model2 := callAPIHandler[routeDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID))

	require.NotEmpty(t, model1.Data.References.Routes)
	require.NotEmpty(t, model1.Data.References.Stops)

	var routeIDs1, routeIDs2 []string
	for _, r := range model1.Data.References.Routes {
		routeIDs1 = append(routeIDs1, r.ID)
	}
	for _, r := range model2.Data.References.Routes {
		routeIDs2 = append(routeIDs2, r.ID)
	}
	assert.Equal(t, routeIDs1, routeIDs2, "route reference order should be stable across requests")
	assert.True(t, sort.StringsAreSorted(routeIDs1), "route references should be sorted by ID")

	var stopIDs1, stopIDs2 []string
	for _, s := range model1.Data.References.Stops {
		stopIDs1 = append(stopIDs1, s.ID)
	}
	for _, s := range model2.Data.References.Stops {
		stopIDs2 = append(stopIDs2, s.ID)
	}
	assert.Equal(t, stopIDs1, stopIDs2, "stop reference order should be stable across requests")
	assert.True(t, sort.StringsAreSorted(stopIDs1), "stop references should be sorted by ID")
}
