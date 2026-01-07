package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entryWrapper, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	entryData, ok := entryWrapper["data"].(map[string]interface{})
	require.True(t, ok)

	entry, ok := entryData["entry"].(map[string]interface{})
	require.True(t, ok)

	if id, exists := entry["id"]; exists {
		assert.NotEmpty(t, id)
	}

	configs, ok := entry["configurations"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, configs)

	config, ok := configs[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, config, "activeServiceIds")
	assert.Contains(t, config, "inactiveServiceIds")
	assert.Contains(t, config, "trips")

	activeServiceIds, ok := config["activeServiceIds"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, activeServiceIds)

	serviceId, ok := activeServiceIds[0].(string)
	require.True(t, ok)
	assert.Contains(t, serviceId, "_")

	trips, ok := config["trips"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, trips)

	trip, ok := trips[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, trip, "tripId")
	assert.Contains(t, trip, "distanceAlongBlock")
	assert.Contains(t, trip, "blockStopTimes")
	assert.Contains(t, trip, "accumulatedSlackTime")

	tripId, ok := trip["tripId"].(string)
	require.True(t, ok)
	assert.Contains(t, tripId, "_")

	_, ok = trip["distanceAlongBlock"].(float64)
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, agencies)

	agency, ok := agencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25", agency["id"])
	assert.Contains(t, agency, "name")
	assert.Contains(t, agency, "url")
	assert.Contains(t, agency, "timezone")

	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, stops)

	stop, ok := stops[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, stop, "id")
	assert.Contains(t, stop, "name")
	assert.Contains(t, stop, "lat")
	assert.Contains(t, stop, "lon")

	routes, ok := refs["routes"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, routes)

	route, ok := routes[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, route, "id")
	assert.Contains(t, route, "agencyId")

	tripsRef, ok := refs["trips"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, tripsRef)

	tripRef, ok := tripsRef[0].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, tripRef, "id")
	assert.Contains(t, tripRef, "routeId")
	assert.Contains(t, tripRef, "serviceId")
}

func TestBlockHandlerVerifyBlockStopTimes(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entryWrapper, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	entryData, ok := entryWrapper["data"].(map[string]interface{})
	require.True(t, ok)

	entry, ok := entryData["entry"].(map[string]interface{})
	require.True(t, ok)

	configs, ok := entry["configurations"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, configs)

	config, ok := configs[0].(map[string]interface{})
	require.True(t, ok)

	trips, ok := config["trips"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, trips)

	trip, ok := trips[0].(map[string]interface{})
	require.True(t, ok)

	blockStopTimes, ok := trip["blockStopTimes"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, blockStopTimes)

	for i, rawStopTime := range []int{0, len(blockStopTimes) - 1} {
		if i >= len(blockStopTimes) {
			continue
		}

		stopTime, ok := blockStopTimes[rawStopTime].(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, stopTime, "blockSequence")
		assert.Contains(t, stopTime, "distanceAlongBlock")
		assert.Contains(t, stopTime, "accumulatedSlackTime")
		assert.Contains(t, stopTime, "stopTime")

		_, ok = stopTime["blockSequence"].(float64)
		require.True(t, ok, "blockSequence should be a number")

		_, ok = stopTime["distanceAlongBlock"].(float64)
		require.True(t, ok, "distanceAlongBlock should be a number")

		_, ok = stopTime["accumulatedSlackTime"].(float64)
		require.True(t, ok, "accumulatedSlackTime should be a number")

		stopTimeDetails, ok := stopTime["stopTime"].(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, stopTimeDetails, "arrivalTime")
		assert.Contains(t, stopTimeDetails, "departureTime")
		assert.Contains(t, stopTimeDetails, "stopId")

		_, ok = stopTimeDetails["arrivalTime"].(float64)
		require.True(t, ok, "arrivalTime should be a number")

		_, ok = stopTimeDetails["departureTime"].(float64)
		require.True(t, ok, "departureTime should be a number")

		stopId, ok := stopTimeDetails["stopId"].(string)
		require.True(t, ok, "stopId should be a string")
		assert.Contains(t, stopId, "_")
	}

	if len(blockStopTimes) >= 2 {
		firstStopTime, ok := blockStopTimes[0].(map[string]interface{})
		require.True(t, ok)
		lastStopTime, ok := blockStopTimes[len(blockStopTimes)-1].(map[string]interface{})
		require.True(t, ok)

		firstSeq, ok := firstStopTime["blockSequence"].(float64)
		require.True(t, ok)
		lastSeq, ok := lastStopTime["blockSequence"].(float64)
		require.True(t, ok)

		assert.Less(t, firstSeq, lastSeq, "blockSequence should increase")

		firstDist, ok := firstStopTime["distanceAlongBlock"].(float64)
		require.True(t, ok)
		lastDist, ok := lastStopTime["distanceAlongBlock"].(float64)
		require.True(t, ok)

		assert.LessOrEqual(t, firstDist, lastDist, "distanceAlongBlock should increase")
	}
}

func TestBlockHandlerMissingBlock(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/block/25_nonexistent.json?key=TEST")
	if resp.StatusCode == http.StatusInternalServerError {
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	} else {
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

func TestBlockHandlerAgencyIdExtraction(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, agencies)

	agency, ok := agencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25", agency["id"])

	assert.Contains(t, agency, "name")
	assert.Contains(t, agency, "url")
	assert.Contains(t, agency, "timezone")
}

func TestBlockHandlerReferencesConsistency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	assert.Contains(t, refs, "agencies")
	assert.Contains(t, refs, "routes")
	assert.Contains(t, refs, "stops")
	assert.Contains(t, refs, "trips")
	assert.Contains(t, refs, "stopTimes")
	assert.Contains(t, refs, "situations")

	entryWrapper, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	entryData, ok := entryWrapper["data"].(map[string]interface{})
	require.True(t, ok)

	entry, ok := entryData["entry"].(map[string]interface{})
	require.True(t, ok)

	configs, ok := entry["configurations"].([]interface{})
	require.True(t, ok)

	if len(configs) > 0 {
		config, ok := configs[0].(map[string]interface{})
		require.True(t, ok)

		trips, ok := config["trips"].([]interface{})
		require.True(t, ok)

		if len(trips) > 0 {
			trip, ok := trips[0].(map[string]interface{})
			require.True(t, ok)

			blockStopTimes, ok := trip["blockStopTimes"].([]interface{})
			require.True(t, ok)

			if len(blockStopTimes) > 0 {
				stopTime, ok := blockStopTimes[0].(map[string]interface{})
				require.True(t, ok)

				stopTimeDetails, ok := stopTime["stopTime"].(map[string]interface{})
				require.True(t, ok)

				stopId, ok := stopTimeDetails["stopId"].(string)
				require.True(t, ok)

				stops, ok := refs["stops"].([]interface{})
				require.True(t, ok)

				found := false
				for _, rawStop := range stops {
					stop, ok := rawStop.(map[string]interface{})
					require.True(t, ok)

					if refStopId, ok := stop["id"].(string); ok && refStopId == stopId {
						found = true
						break
					}
				}

				assert.True(t, found, "Stop %s should be in references", stopId)
			}
		}
	}
}

func TestBlockHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/block/25_1.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}
