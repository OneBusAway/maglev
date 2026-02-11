package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopsForRouteHandlerEndToEnd(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/25_151.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.Greater(t, model.CurrentTime, int64(0))

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25_151", entry["routeId"])

	polylines, ok := entry["polylines"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 2, len(polylines))

	firstPolyline, ok := polylines[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 250, int(firstPolyline["length"].(float64)))
	assert.Equal(t, "", firstPolyline["levels"])
	assert.Contains(t, firstPolyline["points"], "exhwFlt|")

	secondPolyline, ok := polylines[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 250, int(secondPolyline["length"].(float64)))
	assert.Equal(t, "", secondPolyline["levels"])
	assert.Contains(t, secondPolyline["points"], "exhwFlt|")

	stopIds, ok := entry["stopIds"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 39, len(stopIds))
	// Verify stopGroupings
	stopGroupings, ok := entry["stopGroupings"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(stopGroupings))

	grouping, ok := stopGroupings[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, grouping["ordered"])
	assert.Equal(t, "direction", grouping["type"])

	stopGroups, ok := grouping["stopGroups"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 2, len(stopGroups))

	// Verify inbound group (direction 1)
	inboundGroup, ok := stopGroups[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "1", inboundGroup["id"])

	inboundName, ok := inboundGroup["name"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Shasta Lake", inboundName["name"])
	assert.Equal(t, "destination", inboundName["type"])

	inboundNames, ok := inboundName["names"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(inboundNames))
	assert.Equal(t, "Shasta Lake", inboundNames[0])

	inboundStopIds, ok := inboundGroup["stopIds"].([]interface{})
	require.True(t, ok)

	// With deterministic sorting, checks should be consistent
	assert.Equal(t, 21, len(inboundStopIds))

	inboundPolylines, ok := inboundGroup["polylines"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(inboundPolylines))

	// Verify outbound group (direction 0)
	outboundGroup, ok := stopGroups[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "0", outboundGroup["id"])

	outboundName, ok := outboundGroup["name"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Shasta Lake", outboundName["name"])
	assert.Equal(t, "destination", outboundName["type"])

	outboundStopIds, ok := outboundGroup["stopIds"].([]interface{})
	require.True(t, ok)
	// With deterministic sorting, checks should be consistent
	assert.Equal(t, 22, len(outboundStopIds))

	// Verify references
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// Verify agencies
	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(agencies))

	agency, ok := agencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25", agency["id"])
	assert.Equal(t, "Redding Area Bus Authority", agency["name"])
	assert.Equal(t, "http://www.rabaride.com/", agency["url"])
	assert.Equal(t, "America/Los_Angeles", agency["timezone"])
	assert.Equal(t, "en", agency["lang"])
	assert.Equal(t, "530-241-2877", agency["phone"])
	assert.Equal(t, false, agency["privateService"])

	routes, ok := refs["routes"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 13, len(routes))

	// Verify stops
	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 39, len(stops))
	require.True(t, ok)

	// Verify empty arrays
	situations, ok := refs["situations"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, len(situations))

	stopTimes, ok := refs["stopTimes"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, len(stopTimes))

	trips, ok := refs["trips"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, len(trips))
}

func TestStopsForRouteHandlerInvalidRouteID(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/invalid_route.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStopsForRouteHandlerMissingRouteIDComponent(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/_FMS.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStopsForRouteHandlerNonExistentAgency(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/fake_Raba.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStopsForRouteHandlerWithInvalidTimeFormats(t *testing.T) {
	invalidFormats := []string{
		"yesterday",       // Relative time
		"16868172xx",      // Invalid epoch
		"not-a-timestamp", // Random string
		"2099-01-01",      //Time in the future
	}

	for _, format := range invalidFormats {
		t.Run("Invalid format: "+format, func(t *testing.T) {
			_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/25-151.json?key=TEST&time="+format)

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestStopsForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/stops-for-route/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}
