package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/utils"
)

func TestTripHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/invalid.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestTripHandlerEndToEnd(t *testing.T) {

	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]

	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/"+tripID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})

	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, tripID, entry["id"])
	assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].Route.Id), entry["routeId"])
	assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].Service.Id), entry["serviceId"])
	assert.Equal(t, float64(trips[0].DirectionId), entry["directionId"])
	assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].BlockID), entry["blockId"])
	assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].Shape.ID), entry["shapeId"])
	assert.Equal(t, trips[0].Headsign, entry["tripHeadsign"])
	assert.Equal(t, trips[0].ShortName, entry["tripShortName"])
	assert.Equal(t, trips[0].Route.ShortName, entry["routeShortName"])

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok, "References section should exist")
	assert.NotNil(t, references, "References should not be nil")

	routes, ok := references["routes"].([]interface{})
	assert.True(t, ok, "Routes section should exist in references")
	assert.NotEmpty(t, routes, "Routes should not be empty")

	route, ok := routes[0].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].Route.Id), route["id"])
	assert.Equal(t, agency.Id, route["agencyId"])
	assert.Equal(t, trips[0].Route.ShortName, route["shortName"])

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "Agencies section should exist in references")
	assert.NotEmpty(t, agencies, "Agencies should not be empty")

	agencyRef, ok := agencies[0].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, agency.Id, agencyRef["id"])
	assert.Equal(t, agency.Name, agencyRef["name"])
}

func TestTripHandlerWithInvalidTripID(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/agency_invalid.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestTripHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/trip/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}
