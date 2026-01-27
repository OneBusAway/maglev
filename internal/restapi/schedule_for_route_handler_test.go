package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func TestScheduleForRouteHandler(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies, "Test data should contain at least one agency")

	static := api.GtfsManager.GetStaticData()
	require.NotNil(t, static)
	require.NotEmpty(t, static.Routes, "Test data should contain at least one route")

	routeID := utils.FormCombinedID(agencies[0].Id, static.Routes[0].Id)

	t.Run("Valid route", func(t *testing.T) {
		resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/schedule-for-route/"+routeID+".json?key=TEST&date=2025-06-12")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]interface{})
		require.True(t, ok)

		entry, ok := data["entry"].(map[string]interface{})
		require.True(t, ok)

		assert.Equal(t, routeID, entry["routeId"])
		require.NotNil(t, entry["scheduleDate"])

		// serviceIds should exist
		svcIds, ok := entry["serviceIds"].([]interface{})
		require.True(t, ok)
		require.NotEmpty(t, svcIds)

		// stopTripGroupings should exist and have expected structure
		groupings, ok := entry["stopTripGroupings"].([]interface{})
		require.True(t, ok)
		require.NotEmpty(t, groupings)

		firstGrouping, ok := groupings[0].(map[string]interface{})
		require.True(t, ok)

		// Check fields inside grouping
		_, hasDir := firstGrouping["directionId"]
		assert.True(t, hasDir)
		ths, hasTH := firstGrouping["tripHeadsigns"].([]interface{})
		assert.True(t, hasTH)
		assert.NotNil(t, ths)

		stopIds, hasStops := firstGrouping["stopIds"].([]interface{})
		assert.True(t, hasStops)
		assert.NotEmpty(t, stopIds)

		tripIds, hasTrips := firstGrouping["tripIds"].([]interface{})
		assert.True(t, hasTrips)
		assert.NotEmpty(t, tripIds)

		tripsWithStopTimes, hasT := firstGrouping["tripsWithStopTimes"].([]interface{})
		assert.True(t, hasT)
		require.NotEmpty(t, tripsWithStopTimes)

		firstTripWithStops := tripsWithStopTimes[0].(map[string]interface{})
		tid, ok := firstTripWithStops["tripId"].(string)
		require.True(t, ok)
		require.Contains(t, tid, "_", "TripID should be combined with agency prefix")

		stopTimesArr, ok := firstTripWithStops["stopTimes"].([]interface{})
		require.True(t, ok)
		require.NotEmpty(t, stopTimesArr)

		// Check a stop time inside entry trip stopTimes (arrival/departure should be numbers in seconds)
		st0 := stopTimesArr[0].(map[string]interface{})
		arr, ok := st0["arrivalTime"].(float64)
		require.True(t, ok)
		dep, ok := st0["departureTime"].(float64)
		require.True(t, ok)
		require.GreaterOrEqual(t, dep, arr)

		// References should include flattened stopTimes
		refs, ok := data["references"].(map[string]interface{})
		require.True(t, ok)

		stopTimesRef, ok := refs["stopTimes"].([]interface{})
		require.True(t, ok)
		require.NotEmpty(t, stopTimesRef)

		// Validate a reference stopTime contains tripId and stopId combined IDs
		firstRefST := stopTimesRef[0].(map[string]interface{})
		refTid, ok := firstRefST["tripId"].(string)
		require.True(t, ok)
		require.Contains(t, refTid, "_")
		refSid, ok := firstRefST["stopId"].(string)
		require.True(t, ok)
		require.Contains(t, refSid, "_")
		_, hasArrival := firstRefST["arrivalTime"].(float64)
		assert.True(t, hasArrival)
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
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	static := api.GtfsManager.GetStaticData()
	require.NotNil(t, static)
	require.NotEmpty(t, static.Routes)

	routeID := utils.FormCombinedID(agencies[0].Id, static.Routes[0].Id)

	t.Run("Valid date parameter", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]interface{})
		require.True(t, ok)
		entry, ok := data["entry"].(map[string]interface{})
		require.True(t, ok)
		require.NotNil(t, entry["scheduleDate"])
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

func TestScheduleForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/schedule-for-route/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}
