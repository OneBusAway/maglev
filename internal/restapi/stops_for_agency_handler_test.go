package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopsForAgencyRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stops-for-agency/"+agencyID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stops-for-agency/"+agencyID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	// Check list of stops
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	// Verify first stop has expected fields
	firstStop := list[0].(map[string]interface{})
	assert.NotNil(t, firstStop["id"])
	assert.NotNil(t, firstStop["lat"])
	assert.NotNil(t, firstStop["lon"])
	assert.NotNil(t, firstStop["name"])
	assert.NotNil(t, firstStop["code"])
	assert.NotNil(t, firstStop["direction"])
	assert.NotNil(t, firstStop["locationType"])
	assert.NotNil(t, firstStop["routeIds"])
	assert.NotNil(t, firstStop["staticRouteIds"])
	assert.NotNil(t, firstStop["wheelchairBoarding"])

	// Verify stop ID has agency prefix
	stopID := firstStop["id"].(string)
	assert.True(t, strings.HasPrefix(stopID, agencyID+"_"),
		"Stop ID should start with agency ID prefix: %s", stopID)

	// Verify route IDs have agency prefix
	routeIDs := firstStop["routeIds"].([]interface{})
	if len(routeIDs) > 0 {
		routeID := routeIDs[0].(string)
		assert.True(t, strings.HasPrefix(routeID, agencyID+"_"),
			"Route ID should start with agency ID prefix: %s", routeID)
	}

	// Check references
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// Verify agency reference
	agencyRefs, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, agencyRefs, 1)

	// Verify route references exist (may be empty if stops have no routes)
	_, ok = refs["routes"].([]interface{})
	require.True(t, ok)

	// Verify other reference fields exist but are empty
	situations, ok := refs["situations"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, situations)

	stopTimes, ok := refs["stopTimes"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, stopTimes)

	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, stops)

	trips, ok := refs["trips"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, trips)

	// Verify limitExceeded field
	limitExceeded, ok := data["limitExceeded"].(bool)
	require.True(t, ok)
	assert.False(t, limitExceeded)
}

func TestStopsForAgencyInvalidAgency(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-agency/invalid.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
}
