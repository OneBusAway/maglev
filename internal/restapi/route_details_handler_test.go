package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

type RouteDetailsResponse struct {
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

	resp, model := callAPIHandler[RouteDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Len(t, model.Data.List, 1)
	entry := model.Data.List[0]
	assert.Equal(t, testdata.Raba.ID, entry.RouteID.AgencyID)
	assert.Equal(t, "151", entry.RouteID.ID)
	assert.NotNil(t, entry.StopGroupings)
	assert.NotEmpty(t, model.Data.References.Agencies)
	assert.NotEmpty(t, model.Data.References.Routes)
}

func TestRouteDetailsHandler_NotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteDetailsResponse](t, api, routeDetailsURL("1_999999"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

func TestRouteDetailsHandler_InvalidTime(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteDetailsResponse](t, api, routeDetailsURLWithTime(testdata.Route1.ID, "invalid_time"))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRouteDetailsHandler_InvalidRouteIDFormat(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteDetailsResponse](t, api, routeDetailsURL("garbage"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

func TestRouteDetailsHandler_ServiceDateParam(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json?key=TEST&serviceDate=2024-01-01")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
}

func TestRouteDetailsHandler_NoActiveServiceForDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// pick a date far outside the test GTFS feed's service period
	resp, model := callAPIHandler[RouteDetailsResponse](t, api,
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
		if r.ID != "" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestRouteDetailsHandler_NoActiveTrips(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// A date far outside the test GTFS feed's service calendar
	resp, model := callAPIHandler[RouteDetailsResponse](t, api,
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
	resp, model := callAPIHandler[RouteDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)

	// Invalid/unknown key
	resp2, model2 := callAPIHandler[RouteDetailsResponse](t, api,
		"/api/where/route-details/"+testdata.Route1.ID+".json?key=WRONGKEY")
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model2.Code)
}

func TestRouteDetailsHandler_SetsCacheHeaders(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[RouteDetailsResponse](t, api, routeDetailsURL(testdata.Route1.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("ETag"))
	assert.NotEmpty(t, resp.Header.Get("Cache-Control"))
}
