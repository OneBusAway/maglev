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

func TestRoutesForAgencyHandlerInvalidID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "11@10"
	endpoint := "/api/where/routes-for-agency/" + malformedID + ".json?key=TEST"

	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Contains(t, model.Text, "invalid")
}

func TestRoutesForAgencyHandlerNonExistentAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/non-existent-agency.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
	assert.Nil(t, model.Data)
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

		// Check that agencyId is prepended to id
		assert.Contains(t, id, agencyId+"_", "route id must be in {agencyId}_{routeId} format")
	}
}

func TestRoutesForAgencyHandlerPagination(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].Id

	// Case 1: Limit 5 (Should return 5 items, limitExceeded=true)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST&limit=5")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 5)
	assert.True(t, data["limitExceeded"].(bool), "limitExceeded should be true when more items exist")

	// Case 2: Offset 5, Limit 5 (Should return next 5 items)
	resp2, model2 := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST&offset=5&limit=5")
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	data2, ok := model2.Data.(map[string]interface{})
	require.True(t, ok)
	list2, ok := data2["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list2, 5)

	// Verify items are different
	firstItem1 := list[0].(map[string]interface{})
	firstItem2 := list2[0].(map[string]interface{})
	assert.NotEqual(t, firstItem1["id"], firstItem2["id"])

	// Case 3: Limit 100 (Should return all 13 items, limitExceeded=false)
	resp3, model3 := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST&limit=100")
	assert.Equal(t, http.StatusOK, resp3.StatusCode)

	data3, ok := model3.Data.(map[string]interface{})
	require.True(t, ok)
	list3, ok := data3["list"].([]interface{})
	require.True(t, ok)
	// RABA has 13 routes
	assert.Len(t, list3, 13)
	assert.False(t, data3["limitExceeded"].(bool), "limitExceeded should be false when all items returned")
}
