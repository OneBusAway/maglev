package restapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

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

//  Test for non-existent block - should return error
func TestBlockHandlerNonExistentBlock(t *testing.T) {
	api, resp, model := serveAndRetrieveEndpoint(t, "/api/where/block/25_nonexistent.json?key=TEST")
	defer api.Shutdown()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	// Assumes the handler returns "block not found" for this specific case.
	// If the handler uses a generic 404 helper, this might need to be "resource not found".
	assert.Equal(t, "block not found", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.Greater(t, model.CurrentTime, int64(0))
}

//   Test for invalid block ID format
func TestBlockHandlerInvalidBlockID(t *testing.T) {
	testCases := []struct {
		name     string
		endpoint string
	}{
		{"Empty block ID", "/api/where/block/.json?key=TEST"},
		{"Missing agency", "/api/where/block/invalidblock.json?key=TEST"},
		{"Special characters", "/api/where/block/25_@#$.json?key=TEST"},
		{"Only underscore", "/api/where/block/_.json?key=TEST"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			api, resp, model := serveAndRetrieveEndpoint(t, tc.endpoint)
			defer api.Shutdown()

			// Check HTTP status code
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
				"Expected HTTP 400 for invalid block ID: %s", tc.name)

			// Fix: Verify response model structure (ensures API returns valid JSON error, not just "null")
			assert.Equal(t, http.StatusBadRequest, model.Code, "Response model should contain error code 400")
			assert.NotEmpty(t, model.Text, "Response model should contain an error message")
			assert.Equal(t, 2, model.Version, "Response model should contain API version")
		})
	}
}

//   Consolidated test for schema validation and data completeness
func TestBlockHandlerResponseValidation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Validate top-level response structure
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.NotNil(t, model.Data)
	assert.Greater(t, model.CurrentTime, int64(0), "currentTime should be set")
	assert.Equal(t, 2, model.Version, "version should be 2")

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	// Validate entry wrapper structure
	require.Contains(t, data, "entry")
	require.Contains(t, data, "references")

	entryWrapper, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, entryWrapper, "data")

	entryData, ok := entryWrapper["data"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, entryData, "entry")

	// Validate block entry structure
	entry, ok := entryData["entry"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, entry, "id")
	require.Contains(t, entry, "configurations")

	// Verify block ID is not empty
	blockID, ok := entry["id"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, blockID)

	// Validate configurations array
	configs, ok := entry["configurations"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, configs, "Block should have at least one configuration")

	// Validate each configuration
	for _, rawConfig := range configs {
		config, ok := rawConfig.(map[string]interface{})
		require.True(t, ok)

		require.Contains(t, config, "activeServiceIds")
		require.Contains(t, config, "inactiveServiceIds")
		require.Contains(t, config, "trips")

		activeServiceIds, ok := config["activeServiceIds"].([]interface{})
		require.True(t, ok)
		assert.NotEmpty(t, activeServiceIds, "Configuration should have active service IDs")

		// Validate trips array
		trips, ok := config["trips"].([]interface{})
		require.True(t, ok)
		assert.NotEmpty(t, trips, "Configuration should have trips")

		for _, rawTrip := range trips {
			trip, ok := rawTrip.(map[string]interface{})
			require.True(t, ok)

			require.Contains(t, trip, "tripId")
			require.Contains(t, trip, "distanceAlongBlock")
			require.Contains(t, trip, "blockStopTimes")
			require.Contains(t, trip, "accumulatedSlackTime")

			// Validate blockStopTimes
			blockStopTimes, ok := trip["blockStopTimes"].([]interface{})
			require.True(t, ok)
			assert.NotEmpty(t, blockStopTimes, "Trip should have block stop times")

			for _, rawStopTime := range blockStopTimes {
				stopTime, ok := rawStopTime.(map[string]interface{})
				require.True(t, ok)

				require.Contains(t, stopTime, "blockSequence")
				require.Contains(t, stopTime, "distanceAlongBlock")
				require.Contains(t, stopTime, "accumulatedSlackTime")
				require.Contains(t, stopTime, "stopTime")

				// Validate nested stopTime
				st, ok := stopTime["stopTime"].(map[string]interface{})
				require.True(t, ok)
				require.Contains(t, st, "arrivalTime")
				require.Contains(t, st, "departureTime")
				require.Contains(t, st, "stopId")
				require.Contains(t, st, "pickupType")
				require.Contains(t, st, "dropOffType")
			}
		}
	}

	// Validate references structure
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)
	require.Contains(t, refs, "agencies")
	require.Contains(t, refs, "routes")
	require.Contains(t, refs, "stops")
	require.Contains(t, refs, "trips")
	require.Contains(t, refs, "stopTimes")
	require.Contains(t, refs, "situations")
}

//   Test different block IDs
func TestBlockHandlerDifferentBlockIDs(t *testing.T) {
	testCases := []struct {
		name          string
		blockID       string
		shouldSucceed bool
	}{
		{"Valid block ID", "25_1", true},
		// Add more valid block IDs from your test data if available
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			api := createTestApi(t)
			defer api.Shutdown()
			endpoint := "/api/where/block/" + tc.blockID + ".json?key=TEST"
			resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

			if tc.shouldSucceed {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, http.StatusOK, model.Code)
			} else {
				assert.True(t, resp.StatusCode >= 400 || model.Code >= 400)
			}
		})
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
	api, resp, model := serveAndRetrieveEndpoint(t, "/api/where/block/25_1.json?key=invalid")
	defer api.Shutdown()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

// Test missing API key
func TestBlockHandlerMissingApiKey(t *testing.T) {
	api, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/block/25_1.json")
	defer api.Shutdown()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// Performance benchmark test
func BenchmarkBlockHandler(b *testing.B) {
	// Pass 'b' directly now that createTestApi accepts testing.TB
	api := createTestApi(b)
	defer api.Shutdown()
	endpoint := "/api/where/block/25_1.json?key=TEST"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Pass 'b' directly here as well
		_, _ = serveApiAndRetrieveEndpoint(b, api, endpoint)
	}
}

// Test response time is acceptable
func TestBlockHandlerResponseTime(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	start := time.Now()
	resp, _ := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	duration := time.Since(start)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Less(t, duration.Milliseconds(), int64(5000), "Response should be under 5 seconds")
}

// Test JSON marshaling/unmarshaling
func TestBlockHandlerJSONSerialization(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/25_1.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Re-marshal the data to ensure it's valid JSON
	jsonBytes, err := json.Marshal(model)
	require.NoError(t, err, "Should be able to marshal response to JSON")

	// Unmarshal back
	var unmarshaled map[string]interface{}
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	require.NoError(t, err, "Should be able to unmarshal JSON")

	// Verify key fields exist after round-trip
	assert.Contains(t, unmarshaled, "code")
	assert.Contains(t, unmarshaled, "text")
	assert.Contains(t, unmarshaled, "data")
	assert.Contains(t, unmarshaled, "version")
	assert.Contains(t, unmarshaled, "currentTime")
}
