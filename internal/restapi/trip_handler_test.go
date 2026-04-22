package restapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)

	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	ctx := context.Background()
	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	require.NoError(t, err)

	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/"+tripID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]any)

	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, tripID, entry["id"])
	assert.Equal(t, utils.FormCombinedID(agency.ID, trip.RouteID), entry["routeId"])
	assert.Equal(t, utils.FormCombinedID(agency.ID, trip.ServiceID), entry["serviceId"])
	assert.Equal(t, fmt.Sprintf("%d", utils.NullInt64OrDefault(trip.DirectionID, 0)), entry["directionId"])
	assert.Equal(t, utils.FormCombinedID(agency.ID, utils.NullStringOrEmpty(trip.BlockID)), entry["blockId"])
	assert.Equal(t, utils.FormCombinedID(agency.ID, utils.NullStringOrEmpty(trip.ShapeID)), entry["shapeId"])
	assert.Equal(t, utils.NullStringOrEmpty(trip.TripHeadsign), entry["tripHeadsign"])
	assert.Equal(t, utils.NullStringOrEmpty(trip.TripShortName), entry["tripShortName"])
	assert.Equal(t, utils.NullStringOrEmpty(route.ShortName), entry["routeShortName"])

	references, ok := data["references"].(map[string]any)
	assert.True(t, ok, "References section should exist")
	assert.NotNil(t, references, "References should not be nil")

	routes, ok := references["routes"].([]any)
	assert.True(t, ok, "Routes section should exist in references")
	assert.NotEmpty(t, routes, "Routes should not be empty")

	routeRef, ok := routes[0].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, utils.FormCombinedID(agency.ID, trip.RouteID), routeRef["id"])
	assert.Equal(t, agency.ID, routeRef["agencyId"])
	assert.Equal(t, utils.NullStringOrEmpty(route.ShortName), routeRef["shortName"])

	agencies, ok := references["agencies"].([]any)
	assert.True(t, ok, "Agencies section should exist in references")
	assert.NotEmpty(t, agencies, "Agencies should not be empty")

	agencyRef, ok := agencies[0].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, agency.ID, agencyRef["id"])
	assert.Equal(t, agency.Name, agencyRef["name"])
}

func TestTripHandlerWithInvalidTripID(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/agency_invalid.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestTripHandlerWithoutReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/"+tripID+".json?key=TEST&includeReferences=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]any)
	assert.True(t, ok)

	_, hasReferences := data["references"]
	assert.False(t, hasReferences, "references should be omitted when includeReferences=false")

	entry, ok := data["entry"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, tripID, entry["id"])
	assert.Equal(t, utils.FormCombinedID(agency.ID, trip.RouteID), entry["routeId"])
}

func TestTripHandlerWithUnsupportedVersion(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/agency_invalid.json?key=TEST&version=1")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, model.Code)
	assert.Equal(t, "unknown version: 1", model.Text)
	assert.Nil(t, model.Data)
}

func TestTripHandlerWithDefaultVersionExplicitlySet(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip/agency_invalid.json?key=TEST&version=2")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestTripHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/trip/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}
