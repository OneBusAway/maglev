package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestRoutesForLocationHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=invalid&lat=47.586556&lon=-122.190396")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRoutesForLocationHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route19})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationQuery(t *testing.T) {
	api := createTestApi(t)

	// Wider radius includes multiple routes, but query limits response to just 19.
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2000&query=19")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route19})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationLatSpanAndLonSpan(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.01&lonSpan=0.01")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route19})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationRadius(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2000")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, model.Data.List, 2)
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationLatAndLon(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.362535")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Ordering matters! Routes should be sorted by ID.
	assert.EqualValues(t, model.Data.List, []models.Route{testdata.Route15, testdata.Route11, testdata.Route14})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationCaseInsensitiveQuery(t *testing.T) {
	// Lat/Lon are for stop 2000 from the test data, which is on route 44X
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583170&lon=-122.392586&query=44x")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route44x})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationWildcardQueryDoesNotMatch(t *testing.T) {
	// `%` should be treated as a literal character, not a SQL LIKE wildcard.
	// Lat/Lon are for stop 2000 from the test data, which is on route 44X.
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583170&lon=-122.392586&query=%25")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Empty(t, model.Data.List)
}

func TestRoutesForLocationHandlerValidatesParameters(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=invalid&lon=-121.74")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerValidatesLatLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=invalid&lon=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerValidatesLatLonSpan(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=invalid&lonSpan=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerValidatesRadius(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerNoStopsFound(t *testing.T) {
	// Use coordinates far from any stops to trigger the empty stopIDs case
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=0.0&lon=0.0&radius=100")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.Empty(t, model.Data.List)
	assert.False(t, model.Data.LimitExceeded)
	assert.True(t, model.Data.OutOfRange)

	refs := model.Data.References
	assert.Empty(t, refs.Agencies)
	assert.Empty(t, refs.Routes)
	assert.Empty(t, refs.Situations)
	assert.Empty(t, refs.StopTimes)
	assert.Empty(t, refs.Stops)
	assert.Empty(t, refs.Trips)
}

func TestRoutesForLocationHandlerLimitExceeded(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&maxCount=2")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.LimitExceeded)
	// Ordering matters! Routes should be sorted by ID
	assert.EqualValues(t, model.Data.List, []models.Route{testdata.Route15, testdata.Route14})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationHandlerInvalidMaxCount(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.621&lon=-122.571&maxCount=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerMaxCountLessThanOrEqualZero(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.621&lon=-122.571&maxCount=0")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerInRangeWithNoResults(t *testing.T) {
	api := createTestApi(t)
	boundsMap := api.GtfsManager.GetRegionBounds()
	// Pick any agency's bounds for the in-range test
	var bounds gtfs.RegionBounds
	for _, b := range boundsMap {
		bounds = b
		break
	}
	resp, model := callAPIHandler[RoutesResponse](t, api, fmt.Sprintf("/api/where/routes-for-location.json?key=TEST&lat=%v&lon=%v&radius=1", bounds.Lat, bounds.Lon))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, http.StatusOK, model.Code)

	assert.False(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
	assert.Empty(t, model.Data.References.Agencies)
}

func TestRoutesForLocationMissingLat(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lon=-122.426966")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationMissingLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationMissingBothLatAndLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}
