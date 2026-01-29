package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesForAgencyHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRoutesForAgencyHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	// Check that we have a list of routes
	_, ok = data["list"].([]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	refAgencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, refAgencies, 1)
}

func TestRoutesForAgencyHandlerReturnsCompoundRouteIDs(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	routes, ok := data["list"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, routes)

	for _, r := range routes {
		route, ok := r.(map[string]interface{})
		require.True(t, ok)

		id, ok := route["id"].(string)
		require.True(t, ok)

		assert.Contains(t, id, agencyId+"_", "route id must be in {agencyId}_{routeId} format")
	}
}
