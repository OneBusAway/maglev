package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func TestStopHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

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
	defer api.Shutdown()

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
	assert.Equal(t, models.UnknownValue, entry["wheelchairBoarding"])
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
	defer api.Shutdown()

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	invalidStopID := utils.FormCombinedID(agencies[0].Id, "invalid_stop_id")

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/"+invalidStopID+".json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestStopHandlerVerifiesReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	stops := api.GtfsManager.GetStops()
	assert.NotEmpty(t, stops, "Test data should contain at least one stop")

	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/"+stopID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// Verify routes are included
	routes, ok := references["routes"].([]interface{})
	assert.True(t, ok, "Routes should be in references")
	if len(routes) > 0 {
		route, ok := routes[0].(map[string]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, route["id"], "Route should have an ID")
		assert.NotEmpty(t, route["shortName"], "Route should have a short name")
	}

	// Verify agencies are included
	agenciesRef, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "Agencies should be in references")
	if len(agenciesRef) > 0 {
		agency, ok := agenciesRef[0].(map[string]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, agency["id"], "Agency should have an ID")
		assert.NotEmpty(t, agency["name"], "Agency should have a name")
	}

}

func TestStopHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	resp, _ := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/"+malformedID+".json?key=TEST")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestStopHandlerMultiAgencyScenario(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	queries := api.GtfsManager.GtfsDB.Queries

	// 1. Setup Data: Agency A and a Stop belonging to it
	agencyA := "AgencyA"
	stopID := "Stop1"
	_, err := queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       agencyA,
		Name:     "Transit Agency A",
		Url:      "http://agency-a.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:   stopID,
		Name: sql.NullString{String: "Shared Transit Center", Valid: true},
		Lat:  47.6062,
		Lon:  -122.3321,
	})
	require.NoError(t, err)

	// 2. Setup Data: Agency B and a Route belonging to it
	agencyB := "AgencyB"
	routeB_ID := "RouteB"
	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       agencyB,
		Name:     "Transit Agency B",
		Url:      "http://agency-b.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID:        routeB_ID,
		AgencyID:  agencyB,
		ShortName: sql.NullString{String: "B-Line", Valid: true},
		Type:      3, // Bus
	})
	require.NoError(t, err)

	// 3. Setup Data: A Route specifically for Agency A to ensure it appears in references
	routeA_ID := "RouteA"
	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID:        routeA_ID,
		AgencyID:  agencyA,
		ShortName: sql.NullString{String: "A-Line", Valid: true},
		Type:      3,
	})
	require.NoError(t, err)

	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:        "service1",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20250101",
		EndDate:   "20251231",
	})
	require.NoError(t, err)

	// 4. Link them: Create Trips and StopTimes for both agencies at the shared stop
	// Trip for Agency B (Arriving at 08:00:00 -> 28800 seconds)
	tripB_ID := "TripB"
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:        tripB_ID,
		RouteID:   routeB_ID,
		ServiceID: "service1",
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripB_ID,
		StopID:        stopID,
		StopSequence:  1,
		ArrivalTime:   28800, // 08:00:00
		DepartureTime: 29100, // 08:05:00
	})
	require.NoError(t, err)

	// Trip for Agency A (Arriving at 09:00:00 -> 32400 seconds)
	tripA_ID := "TripA"
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:        tripA_ID,
		RouteID:   routeA_ID,
		ServiceID: "service1",
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripA_ID,
		StopID:        stopID,
		StopSequence:  1,
		ArrivalTime:   32400, // 09:00:00
		DepartureTime: 32700, // 09:05:00
	})
	require.NoError(t, err)

	// 5. Execution: Request the stop using Agency A's prefix
	endpoint := "/api/where/stop/" + agencyA + "_" + stopID + ".json?key=TEST"
	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

	// 6. Assertions
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	// Assert Route IDs use their respective correct Agency prefixes (The fix verification)
	routeIDs, ok := entry["routeIds"].([]interface{})
	require.True(t, ok)
	assert.Contains(t, routeIDs, agencyB+"_"+routeB_ID, "Route B ID should be prefixed with Agency B")
	assert.Contains(t, routeIDs, agencyA+"_"+routeA_ID, "Route A ID should be prefixed with Agency A")

	// Assert references.agencies contains BOTH Agency A and Agency B
	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)
	agencies, ok := references["agencies"].([]interface{})
	require.True(t, ok)

	foundA := false
	foundB := false
	for _, a := range agencies {
		agencyMap := a.(map[string]interface{})
		if agencyMap["id"] == agencyA {
			foundA = true
		}
		if agencyMap["id"] == agencyB {
			foundB = true
		}
	}
	assert.True(t, foundA, "Agency A should be in references")
	assert.True(t, foundB, "Agency B should be in references")
}
