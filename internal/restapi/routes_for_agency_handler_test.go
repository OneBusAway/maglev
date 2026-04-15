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
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRoutesForAgencyHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	routes, ok := data["list"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, routes)

	for _, r := range routes {
		route, ok := r.(map[string]interface{})
		require.True(t, ok)
		assert.Nil(t, route["nullSafeShortName"], "nullSafeShortName must not appear in API responses")
		assert.NotEmpty(t, route["id"], "route id must be present")
		assert.NotEmpty(t, route["agencyId"], "route agencyId must be present")
		_, hasType := route["type"]
		assert.True(t, hasType, "route type must be present")
	}

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	refAgencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	require.Len(t, refAgencies, 1)

	agency, ok := refAgencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, agency["id"], "agency id must be present")
	assert.NotEmpty(t, agency["name"], "agency name must be present")
	assert.NotEmpty(t, agency["url"], "agency url must be present")
	assert.NotEmpty(t, agency["timezone"], "agency timezone must be present")
	_, hasPrivateService := agency["privateService"]
	assert.True(t, hasPrivateService, "agency privateService must be present")
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

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestRoutesForAgencyHandlerReturnsCompoundRouteIDs(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

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

func TestRoutesForAgencyHandlerLimitExceededAlwaysFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	limitExceeded, ok := data["limitExceeded"].(bool)
	require.True(t, ok)
	assert.False(t, limitExceeded)
}

func TestRoutesForAgencyHandlerIncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/routes-for-agency/"+agencyId+".json?key=TEST&includeReferences=false")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	if refs, ok := data["references"].(map[string]interface{}); ok {
		refAgencies, _ := refs["agencies"].([]interface{})
		assert.Empty(t, refAgencies, "agencies must be empty when includeReferences=false")
	}
}
