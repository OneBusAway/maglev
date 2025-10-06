package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesForLocationHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=invalid&lat=47.586556&lon=-122.190396")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRoutesForLocationHandlerEndToEnd(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	route, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, route, "id")
	assert.Contains(t, route, "agencyId")
	assert.Contains(t, route, "shortName")
	assert.Contains(t, route, "longName")
	assert.Contains(t, route, "type")

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, agencies)

	agency, ok := agencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, agency, "id")
	assert.Contains(t, agency, "name")
	assert.Contains(t, agency, "url")
	assert.Contains(t, agency, "timezone")
	assert.Contains(t, agency, "lang")
	assert.Contains(t, agency, "phone")
}

func TestRoutesForLocationQuery(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=19")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 1)

	route, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "19", route["shortName"])
	assert.Equal(t, "Route 19", route["longName"])
}

func TestRoutesForLocationLatSpanAndLonSpan(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.01&lonSpan=0.01")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 1)
	route, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "19", route["shortName"])
	assert.Equal(t, "Route 19", route["longName"])
}

func TestRoutesForLocationRadius(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 2)
}

func TestRoutesForLocationLatAndLon(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.362535")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 3)
}

func TestRoutesForLocationHandlerValidatesParameters(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=invalid&lon=-121.74")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRoutesForLocationHandlerValidatesLatLon(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=invalid&lon=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRoutesForLocationHandlerValidatesLatLonSpan(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=invalid&lonSpan=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRoutesForLocationHandlerValidatesRadius(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRoutesForLocationHandlerNoStopsFound(t *testing.T) {
	// Use coordinates far from any stops to trigger the empty stopIDs case
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=TEST&lat=0.0&lon=0.0&radius=100")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	// Verify empty list
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, list)

	// Verify limitExceeded is false
	limitExceeded, ok := data["limitExceeded"].(bool)
	require.True(t, ok)
	assert.False(t, limitExceeded)

	// Verify outOfRange is true (no stops found in the location)
	outOfRange, ok := data["outOfRange"].(bool)
	require.True(t, ok)
	assert.True(t, outOfRange)

	// Verify references structure
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// All reference arrays should be empty or null
	// Agencies can be nil (null in JSON) or empty array
	agencies := refs["agencies"]
	if agencies != nil {
		agenciesList, ok := agencies.([]interface{})
		require.True(t, ok)
		assert.Empty(t, agenciesList)
	}

	routes, ok := refs["routes"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, routes)

	situations, ok := refs["situations"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, situations)

	stopTimes, ok := refs["stopTimes"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, stopTimes)

	stops := refs["stops"]
	if stops != nil {
		stopsList, ok := stops.([]interface{})
		require.True(t, ok)
		assert.Empty(t, stopsList)
	}

	trips, ok := refs["trips"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, trips)
}
