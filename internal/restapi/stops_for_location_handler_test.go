package restapi

import (
	"fmt" // for fmt.Sprintf
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopsForLocationHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=invalid&lat=47.586556&lon=-122.190396")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopsForLocationHandlerEndToEnd(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966")

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
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.01&lonSpan=0.01")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 2)
	stop, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2042", stop["code"])
	assert.Equal(t, "Buenaventura Blvd at Eureka Way", stop["name"])
}

func TestStopsForLocationRadius(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 77)
}

func TestStopsForLocationLatAndLan(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.362535")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 12)
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

func TestStopsForLocationReturnsDirection(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, _ := model.Data.(map[string]interface{})
	list := data["list"].([]interface{})
	require.NotEmpty(t, list)

	stop := list[0].(map[string]interface{})
	dir := stop["direction"].(string)

	// Direction should exist
	require.NotEmpty(t, dir)

	// It should be either a compass direction or NA
	valid := map[string]bool{
		"N": true, "NE": true, "E": true, "SE": true,
		"S": true, "SW": true, "W": true, "NW": true,
		"UNKNOWN": true, // <--- correct fallback for THIS repo
	}
	assert.True(t, valid[dir], "unexpected direction: %v", dir)

}

func TestStopsForLocationMatchesStopsForRoute(t *testing.T) {
	// Call stops-for-location
	_, _, locModel := serveAndRetrieveEndpoint(t, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=2042")
	locList := locModel.Data.(map[string]interface{})["list"].([]interface{})
	require.Equal(t, 1, len(locList))
	locStop := locList[0].(map[string]interface{})
	locDir := locStop["direction"].(string)

	// Extract a valid routeId from the stop
	routeIds := locStop["routeIds"].([]interface{})
	require.NotEmpty(t, routeIds)

	routeID := routeIds[0].(string)

	// Call stops-for-route for that route
	url := fmt.Sprintf("/api/where/stops-for-route/%s.json?key=TEST", routeID)

	_, _, routeModel := serveAndRetrieveEndpoint(t, url)

	refs := routeModel.Data.(map[string]interface{})["references"].(map[string]interface{})
	stops := refs["stops"].([]interface{})
	require.NotEmpty(t, stops)
	// Find the SAME stop (2042) in the stops-for-route list
	var routeDir string
	for _, s := range stops {
		stopMap := s.(map[string]interface{})
		if stopMap["id"] == locStop["id"] {
			routeDir = stopMap["direction"].(string)
			break
		}
	}

	require.NotEmpty(t, routeDir)

	// Directions must match across endpoints
	assert.Equalf(t, locDir, routeDir,
		"direction mismatch: location=%s route=%s", locDir, routeDir)

}
