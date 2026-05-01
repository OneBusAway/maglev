package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func TestRouteHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	agencies := mustGetAgencies(t, api)
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := mustGetRoutes(t, api)
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(routes[0].AgencyID, routes[0].ID)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	agencies := mustGetAgencies(t, api)
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := mustGetRoutes(t, api)
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(routes[0].AgencyID, routes[0].ID)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]any)
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]any)
	assert.True(t, ok)

	assert.Equal(t, routeID, entry["id"])
	assert.Equal(t, routes[0].AgencyID, entry["agencyId"])
	assert.Equal(t, routes[0].ShortName.String, entry["shortName"])
	assert.Equal(t, routes[0].LongName.String, entry["longName"])
	assert.Equal(t, routes[0].Desc.String, entry["description"])
	assert.Equal(t, routes[0].Url.String, entry["url"])
	assert.Equal(t, routes[0].Color.String, entry["color"])
	assert.Equal(t, routes[0].TextColor.String, entry["textColor"])
	if typeVal, typeOk := entry["type"].(float64); typeOk {
		assert.Equal(t, int(routes[0].Type), int(typeVal))
	} else {
		assert.Fail(t, "Route type missing or not a number")
	}

	references, ok := data["references"].(map[string]any)
	assert.True(t, ok, "References section should exist")
	assert.NotEmpty(t, references, "References section should not be nil")

	agenciesRef, ok := references["agencies"].([]any)
	assert.True(t, ok, "Agencies reference should exist and be an array")
	agencyRef := agenciesRef[0].(map[string]any)
	assert.Equal(t, agencies[0].ID, agencyRef["id"])
	assert.NotEmpty(t, agenciesRef, "Agencies reference should not be empty")
}

func TestInvalidRouteID(t *testing.T) {
	api := createTestApi(t)

	agencies := mustGetAgencies(t, api)
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := mustGetRoutes(t, api)
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	invalidRouteID := utils.FormCombinedID(routes[0].AgencyID, "invalid_route_id")

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+invalidRouteID+".json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestRouteHandlerVerifiesReferences(t *testing.T) {
	api := createTestApi(t)

	agencies := mustGetAgencies(t, api)
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := mustGetRoutes(t, api)
	assert.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(routes[0].AgencyID, routes[0].ID)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok)

	references, ok := data["references"].(map[string]any)
	require.True(t, ok)

	// Verify agencies are included
	agenciesRef, ok := references["agencies"].([]any)
	assert.True(t, ok, "Agencies should be in references")
	if len(agenciesRef) > 0 {
		agency, ok := agenciesRef[0].(map[string]any)
		assert.True(t, ok)
		assert.NotEmpty(t, agency["id"], "Agency should have an ID")
		assert.Equal(t, routes[0].AgencyID, agency["id"])
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

func TestRouteHandlerWithSituations(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	routes := mustGetRoutes(t, api)
	require.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := routes[0].ID
	agencyID := routes[0].AgencyID
	combinedRouteID := utils.FormCombinedID(agencyID, routeID)

	alert := gtfs.Alert{
		ID: "test-alert-123",
		InformedEntities: []gtfs.AlertInformedEntity{
			{RouteID: &routeID},
		},
		Header: []gtfs.AlertText{
			{Text: "Test Route Alert", Language: "en"},
		},
	}

	api.GtfsManager.AddAlertForTest(alert)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+combinedRouteID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok, "Response should have a data object")

	references, ok := data["references"].(map[string]any)
	require.True(t, ok, "Data should have a references object")

	situations, ok := references["situations"].([]any)
	require.True(t, ok, "References should have a situations array")

	require.NotEmpty(t, situations, "Situations array should NOT be empty when alerts exist")
	require.Len(t, situations, 1, "Should have exactly 1 situation")

	situationMap := situations[0].(map[string]any)
	assert.Equal(t, "test-alert-123", situationMap["id"], "The alert ID should match our mocked alert")
}

func TestRouteHandlerAgencyNotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	routes, err := api.GtfsManager.GetRoutes(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, routes, "Test data should contain at least one route")

	// Use a valid route ID but with a non-existent agency ID
	invalidAgencyID := "nonexistent_agency"
	routeID := utils.FormCombinedID(invalidAgencyID, routes[0].ID)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route/"+routeID+".json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}
