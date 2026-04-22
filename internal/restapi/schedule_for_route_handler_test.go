package restapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

func TestScheduleForRouteHandler(t *testing.T) {

	clk := clock.NewMockClock(time.Date(2025, 12, 26, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies, "Test data should contain at least one agency")

	routes := mustGetRoutes(t, api)
	require.NotEmpty(t, routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(agencies[0].ID, routes[0].ID)

	t.Run("Valid route", func(t *testing.T) {
		// Use a date known to be in the test data's service calendar
		resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/schedule-for-route/"+routeID+".json?key=TEST&date=2025-06-12")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]any)
		require.True(t, ok)

		entry, ok := data["entry"].(map[string]any)
		require.True(t, ok)

		// ScheduleForRouteEntry has only: routeId, scheduleDate, serviceIds, stopTripGroupings (no top-level stops/trips)
		assert.Equal(t, routeID, entry["routeId"])
		scheduleDate, ok := entry["scheduleDate"].(float64)
		require.True(t, ok, "scheduleDate should be a numeric Unix millisecond timestamp")
		assert.Greater(t, scheduleDate, float64(0))

		// serviceIds should exist
		svcIds, ok := entry["serviceIds"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, svcIds)

		// stopTripGroupings should exist and have expected structure
		groupings, ok := entry["stopTripGroupings"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, groupings)

		firstGrouping, ok := groupings[0].(map[string]any)
		require.True(t, ok)

		// Check fields inside grouping
		directionId, hasDir := firstGrouping["directionId"].(string)
		assert.True(t, hasDir, "directionId should be a string")
		assert.NotEmpty(t, directionId)
		ths, hasTH := firstGrouping["tripHeadsigns"].([]any)
		assert.True(t, hasTH)
		assert.NotNil(t, ths)

		stopIds, hasStops := firstGrouping["stopIds"].([]any)
		assert.True(t, hasStops)
		assert.NotEmpty(t, stopIds)

		tripIds, hasTrips := firstGrouping["tripIds"].([]any)
		assert.True(t, hasTrips)
		assert.NotEmpty(t, tripIds)

		tripsWithStopTimes, hasT := firstGrouping["tripsWithStopTimes"].([]any)
		assert.True(t, hasT)
		require.NotEmpty(t, tripsWithStopTimes)

		firstTripWithStops := tripsWithStopTimes[0].(map[string]any)
		tid, ok := firstTripWithStops["tripId"].(string)
		require.True(t, ok)
		require.Contains(t, tid, "_", "TripID should be combined with agency prefix")

		stopTimesArr, ok := firstTripWithStops["stopTimes"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, stopTimesArr)

		// Check a stop time inside entry trip stopTimes (arrival/departure should be numbers in seconds)
		st0 := stopTimesArr[0].(map[string]any)
		arr, ok := st0["arrivalTime"].(float64)
		require.True(t, ok)
		dep, ok := st0["departureTime"].(float64)
		require.True(t, ok)
		require.GreaterOrEqual(t, dep, arr)

		// References should include flattened stopTimes
		refs, ok := data["references"].(map[string]any)
		require.True(t, ok)

		stopTimesRef, ok := refs["stopTimes"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, stopTimesRef)

		// Validate a reference stopTime contains stopId combined IDs
		firstRefST := stopTimesRef[0].(map[string]any)

		refTid, ok := firstRefST["tripId"].(string)
		require.True(t, ok, "tripId should be present and be a string")
		require.Contains(t, refTid, "_", "tripId should be a combined ID")

		refSid, ok := firstRefST["stopId"].(string)
		require.True(t, ok, "stopId should be present and be a string")
		require.Contains(t, refSid, "_", "stopId should be a combined ID")

		_, hasArrival := firstRefST["arrivalTime"].(float64)
		assert.True(t, hasArrival, "arrivalTime should be present")
	})

	t.Run("Invalid route", func(t *testing.T) {
		resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/schedule-for-route/"+routeID+"notexist.json?key=TEST")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		// model.Code may be 0 in some error paths; only assert if set
		if model.Code != 0 {
			assert.Equal(t, http.StatusNotFound, model.Code)
		}
	})
}

func TestScheduleForRouteHandlerDateParam(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	routes := mustGetRoutes(t, api)
	require.NotEmpty(t, routes)

	routeID := utils.FormCombinedID(agencies[0].ID, routes[0].ID)

	t.Run("Valid date parameter", func(t *testing.T) {
		// Use a date known to be in the test data's service calendar
		endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]any)
		require.True(t, ok)
		entry, ok := data["entry"].(map[string]any)
		require.True(t, ok)
		scheduleDate, ok := entry["scheduleDate"].(float64)
		require.True(t, ok, "scheduleDate should be a numeric Unix millisecond timestamp")
		assert.Greater(t, scheduleDate, float64(0))
	})

	t.Run("Invalid date format", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025/06/12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		if model.Code != 0 {
			assert.Equal(t, http.StatusBadRequest, model.Code)
		}
	})
}

// Regression for #790: serviceIds must be derived from the route's actual
// trips, not from the agency's active service IDs for the day. Route 25_1885
// uses only c_868_b_79978_d_31, while several other services are active on
// the same weekday — the response must include only the route-scoped set.
func TestScheduleForRouteHandler_ServiceIDsScopedToRoute(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	routeID := utils.FormCombinedID("25", "1885")
	expectedServiceID := utils.FormCombinedID("25", "c_868_b_79978_d_31")

	endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	svcIdsRaw, ok := entry["serviceIds"].([]interface{})
	require.True(t, ok)

	svcIds := make([]string, 0, len(svcIdsRaw))
	for _, v := range svcIdsRaw {
		s, ok := v.(string)
		require.True(t, ok)
		svcIds = append(svcIds, s)
	}

	assert.ElementsMatch(t, []string{expectedServiceID}, svcIds,
		"serviceIds must be scoped to the route's trips, not agency-wide active services")
}

func TestScheduleForRouteHandler_DirectionIDMatchesCSV(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	routeID := utils.FormCombinedID("25", "1885")
	endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	data, _ := model.Data.(map[string]interface{})
	entry, _ := data["entry"].(map[string]interface{})
	groupings, _ := entry["stopTripGroupings"].([]interface{})
	require.Len(t, groupings, 2)

	refs, _ := data["references"].(map[string]interface{})
	tripRefs, _ := refs["trips"].([]interface{})
	tripDirByID := make(map[string]string, len(tripRefs))
	for _, tr := range tripRefs {
		trMap := tr.(map[string]interface{})
		tid, _ := trMap["id"].(string)
		dir, _ := trMap["directionId"].(string)
		tripDirByID[tid] = dir
	}

	first, _ := groupings[0].(map[string]interface{})
	second, _ := groupings[1].(map[string]interface{})
	assert.Equal(t, "0", first["directionId"])
	assert.Equal(t, "1", second["directionId"])

	for _, g := range groupings {
		gMap := g.(map[string]interface{})
		gid, _ := gMap["directionId"].(string)
		tripIDs, _ := gMap["tripIds"].([]interface{})
		require.NotEmpty(t, tripIDs)
		for _, tid := range tripIDs {
			ts, _ := tid.(string)
			assert.Equal(t, gid, tripDirByID[ts],
				"group %q trip %q should have CSV direction_id %q, got %q", gid, ts, gid, tripDirByID[ts])
		}
	}
}

func TestScheduleForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/schedule-for-route/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}
