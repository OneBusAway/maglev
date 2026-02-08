package restapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

func TestStopsForLocationHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=invalid&lat=47.586556&lon=-122.190396")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopsForLocationHandlerEndToEnd(t *testing.T) {
	// Mock clock set to Dec 26, 2025. This date was chosen by evaluating the test
	// criteria: we need a day with active stops within the queried location.
	// Any date that satisfies the test requirements against the test GTFS data can be used
	// in the test.

	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 00, 00, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	stop, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, stop, "id")
	assert.Contains(t, stop, "code")
	assert.Contains(t, stop, "name")
	assert.Contains(t, stop, "lat")
	assert.Contains(t, stop, "lon")
	assert.Contains(t, stop, "direction")
	assert.Contains(t, stop, "routeIds")
	assert.Contains(t, stop, "staticRouteIds")
	assert.Contains(t, stop, "wheelchairBoarding")

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

	routes, ok := refs["routes"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, routes)

	route, ok := routes[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, route, "id")
	assert.Contains(t, route, "agencyId")
	assert.Contains(t, route, "shortName")
	assert.Contains(t, route, "longName")
	assert.Contains(t, route, "type")

	referencedRouteIds := collectAllNestedIdsFromObjects(t, list, "routeIds")
	referencedRouteIds = append(referencedRouteIds, collectAllNestedIdsFromObjects(t, list, "staticRouteIds")...)
	require.NotEmpty(t, referencedRouteIds, "Test data must have route references to verify")
	routeIds := collectAllIdsFromObjects(t, routes, "id")
	for _, routeId := range referencedRouteIds {
		assert.Contains(t, routeIds, routeId, "Stop routeId should reference known route")
	}

	referencedAgencyIds := collectAllIdsFromObjects(t, routes, "agencyId")
	require.NotEmpty(t, referencedAgencyIds, "Test data must have agency references to verify")
	agencyIds := collectAllIdsFromObjects(t, agencies, "id")
	for _, agencyId := range referencedAgencyIds {
		assert.Contains(t, agencyIds, agencyId, "Route agencyId should reference known agency")
	}

	assert.Empty(t, refs["situations"])
	assert.Empty(t, refs["stopTimes"])
	assert.Empty(t, refs["stops"])
	assert.Empty(t, refs["trips"])
}

func TestStopsForLocationQuery(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=2042")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 1)

	stop, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2042", stop["code"])
	assert.Equal(t, "Buenaventura Blvd at Eureka Way", stop["name"])
}

func TestStopsForLocationLatSpanAndLonSpan(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.045&lonSpan=0.059")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)
	stop, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, stop)
}

func TestStopsForLocationRadius(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)
}

func TestStopsForLocationLatAndLan(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&radius=1000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)
}

func TestStopsForLocationHandlerValidatesParameters(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=invalid&lon=-121.74")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStopsForLocationHandlerValidatesLatLon(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=invalid&lon=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStopsForLocationHandlerValidatesLatLonSpan(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=invalid&lonSpan=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStopsForLocationHandlerValidatesRadius(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStopsForLocationHandlerRouteTypeErrorLimit(t *testing.T) {
	invalidTypes := strings.Repeat("bad,", 14) + "bad"

	url := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=" + invalidTypes
	_, resp, model := serveAndRetrieveEndpoint(t, url)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response data should be a map")

	fieldErrors, ok := data["fieldErrors"].(map[string]interface{})
	require.True(t, ok, "data should contain fieldErrors map")

	routeTypeErrors, ok := fieldErrors["routeType"].([]interface{})
	require.True(t, ok, "fieldErrors should contain routeType errors list")

	assert.Len(t, routeTypeErrors, 10, "Should cap validation errors at 10 to prevent DoS")

	for _, err := range routeTypeErrors {
		errStr, ok := err.(string)
		require.True(t, ok)
		assert.Contains(t, errStr, "Invalid field value for field", "Error should use standard generic message")
	}
}

func TestStopsForLocationHandlerRouteTypeTooManyTokens(t *testing.T) {
	tokens := make([]string, 150)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("%d", i)
	}
	manyTokens := strings.Join(tokens, ",")

	url := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=" + manyTokens
	_, resp, model := serveAndRetrieveEndpoint(t, url)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response data should be a map")

	fieldErrors, ok := data["fieldErrors"].(map[string]interface{})
	require.True(t, ok, "data should contain fieldErrors map")

	routeTypeErrors, ok := fieldErrors["routeType"].([]interface{})
	require.True(t, ok, "fieldErrors should contain routeType errors list")

	assert.Len(t, routeTypeErrors, 1, "Should return single error for too many tokens")

	firstError, ok := routeTypeErrors[0].(string)
	require.True(t, ok)
	assert.Contains(t, firstError, "too many route types", "Error should mention the token limit")
}

func TestStopsForLocationHandlerRouteTypeAtLimit(t *testing.T) {
	tokens := make([]string, 100)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("%d", i)
	}
	validTypes := strings.Join(tokens, ",")

	url := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=" + validTypes
	_, resp, _ := serveAndRetrieveEndpoint(t, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "100 tokens should be accepted (at the limit)")
}

func TestStopsForLocationHandlerRouteTypeMixedValidInvalid(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=1,bad,2,invalid,3")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response data should be a map")

	fieldErrors, ok := data["fieldErrors"].(map[string]interface{})
	require.True(t, ok, "data should contain fieldErrors map")

	routeTypeErrors, ok := fieldErrors["routeType"].([]interface{})
	require.True(t, ok, "fieldErrors should contain routeType errors list")

	assert.Len(t, routeTypeErrors, 2, "Should report both invalid tokens")

	for _, err := range routeTypeErrors {
		errStr, ok := err.(string)
		require.True(t, ok)
		assert.Contains(t, errStr, "Invalid field value for field", "Error should use standard generic message")
	}
}

func TestStopsForLocationHandlerRouteTypeValidMultiple(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=1,2,3")

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Valid route types should be accepted")

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotNil(t, list) 

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)
	assert.NotNil(t, refs["agencies"])
	assert.NotNil(t, refs["routes"])
}
