package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func TestRouteHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := api.GtfsManager.GetRoutes()
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(routes[0].Agency.Id, routes[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := api.GtfsManager.GetRoutes()
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(routes[0].Agency.Id, routes[0].Id)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	assert.Equal(t, routeID, entry["id"])
	assert.Equal(t, routes[0].Agency.Id, entry["agencyId"])
	assert.Equal(t, routes[0].ShortName, entry["shortName"])
	assert.Equal(t, routes[0].LongName, entry["longName"])
	assert.Equal(t, routes[0].Description, entry["description"])
	assert.Equal(t, routes[0].Url, entry["url"])
	assert.Equal(t, routes[0].Color, entry["color"])
	assert.Equal(t, routes[0].TextColor, entry["textColor"])
	assert.Equal(t, int(routes[0].Type), int(entry["type"].(float64)))

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok, "References section should exist")
	assert.NotEmpty(t, references, "References section should not be nil")

	agenciesRef, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "Agencies reference should exist and be an array")
	agencyRef := agenciesRef[0].(map[string]interface{})
	assert.Equal(t, agencies[0].Id, agencyRef["id"])
	assert.NotEmpty(t, agenciesRef, "Agencies reference should not be empty")
}

func TestInvalidRouteID(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := api.GtfsManager.GetRoutes()
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	invalidRouteID := utils.FormCombinedID(routes[0].Agency.Id, "invalid_route_id")

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+invalidRouteID+".json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestRouteHandlerVerifiesReferences(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := api.GtfsManager.GetRoutes()
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(routes[0].Agency.Id, routes[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// Verify agencies are included
	agenciesRef, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "Agencies should be in references")
	if len(agenciesRef) > 0 {
		agency, ok := agenciesRef[0].(map[string]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, agency["id"], "Agency should have an ID")
		assert.Equal(t, routes[0].Agency.Id, agency["id"])
		assert.NotEmpty(t, agency["name"], "Agency should have a name")
	}
}

func TestRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1-SHUTTLE"
	resp, _ := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+malformedID+".json?key=TEST")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}
