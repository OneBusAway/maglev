package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/utils"
)

func TestStopHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	stops := api.GtfsManager.GetStops()
	assert.NotEmpty(t, stops, "Test data should contain at least one stop")

	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/"+stopID+".json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	stops := api.GtfsManager.GetStops()
	assert.NotEmpty(t, stops, "Test data should contain at least one stop")

	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/"+stopID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, stopID, entry["id"])
	assert.Equal(t, stops[0].Name, entry["name"])
	assert.Equal(t, stops[0].Code, entry["code"])
	assert.Equal(t, "UNKNOWN", entry["wheelchairBoarding"])
	assert.Equal(t, *stops[0].Latitude, entry["lat"])
	assert.Equal(t, *stops[0].Longitude, entry["lon"])

	routeIds, ok := entry["routeIds"].([]interface{})
	assert.True(t, ok, "routeIds should exist and be an array")
	assert.NotEmpty(t, routeIds, "routeIds should not be empty")

	staticRouteIds, ok := entry["staticRouteIds"].([]interface{})
	assert.True(t, ok, "staticRouteIds should exist and be an array")
	assert.NotEmpty(t, staticRouteIds, "staticRouteIds should not be empty")

	assert.Equal(t, len(routeIds), len(staticRouteIds), "routeIds and staticRouteIds should have same length")

	references, ok := data["references"].(map[string]interface{})

	assert.True(t, ok, "References section should exist")
	assert.NotNil(t, references, "References should not be nil")

	routes, ok := references["routes"].([]interface{})
	assert.True(t, ok, "Routes section should exist in references")
	assert.Equal(t, len(routeIds), len(routes), "Number of routes in references should match routeIds")
}

func TestInvalidStopID(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	invalidStopID := utils.FormCombinedID(agencies[0].Id, "invalid_stop_id")

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/"+invalidStopID+".json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}
