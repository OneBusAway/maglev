package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// blockURL builds the /block endpoint URL with key=TEST baked in. Tests that
// want a different key (auth checks) build their URL inline.
func blockURL(blockID string) string {
	return "/api/where/block/" + blockID + ".json?key=TEST"
}

func TestBlockHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[BlockEntryResponse](t, api, blockURL("25_1"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.Greater(t, model.CurrentTime, int64(0))

	entry := model.Data.Entry
	assert.NotEmpty(t, entry.ID)
	require.NotEmpty(t, entry.Configurations)

	// Detailed checks on the first config/trip — verifies structure and ID formatting.
	config := entry.Configurations[0]
	require.NotEmpty(t, config.ActiveServiceIds)
	assert.Contains(t, config.ActiveServiceIds[0], "_", "service IDs should be combined with agency prefix")
	require.NotEmpty(t, config.Trips)

	trip := config.Trips[0]
	assert.Contains(t, trip.TripId, "_", "trip ID should be combined with agency prefix")
	assert.NotZero(t, trip.DistanceAlongBlock)
	assert.NotEmpty(t, trip.BlockStopTimes)

	// Iterate every config/trip/stop-time — catches empty IDs in less-common configurations.
	for _, c := range entry.Configurations {
		assert.NotEmpty(t, c.ActiveServiceIds)
		assert.NotEmpty(t, c.Trips)
		for _, tr := range c.Trips {
			assert.NotEmpty(t, tr.TripId)
			assert.NotEmpty(t, tr.BlockStopTimes)
			for _, st := range tr.BlockStopTimes {
				assert.NotEmpty(t, st.StopTime.StopID)
			}
		}
	}

	refs := model.Data.References
	idx := slices.IndexFunc(refs.Agencies, func(a models.AgencyReference) bool {
		return a.ID == testdata.Raba.ID
	})
	require.GreaterOrEqual(t, idx, 0, "agency %s should be in references", testdata.Raba.ID)
	agency := refs.Agencies[idx]
	assert.Equal(t, testdata.Raba.Name, agency.Name)
	assert.Equal(t, testdata.Raba.URL, agency.URL)
	assert.Equal(t, testdata.Raba.Timezone, agency.Timezone)

	require.NotEmpty(t, refs.Stops)
	assert.NotEmpty(t, refs.Stops[0].ID)
	assert.NotEmpty(t, refs.Stops[0].Name)

	require.NotEmpty(t, refs.Routes)
	assert.NotEmpty(t, refs.Routes[0].ID)
	assert.NotEmpty(t, refs.Routes[0].AgencyID)

	require.NotEmpty(t, refs.Trips)
	assert.NotEmpty(t, refs.Trips[0].ID)
	assert.NotEmpty(t, refs.Trips[0].RouteID)
	assert.NotEmpty(t, refs.Trips[0].ServiceID)
}

func TestBlockHandlerVerifyBlockStopTimes(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[BlockEntryResponse](t, api, blockURL("25_1"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.Entry.Configurations)
	require.NotEmpty(t, model.Data.Entry.Configurations[0].Trips)
	blockStopTimes := model.Data.Entry.Configurations[0].Trips[0].BlockStopTimes
	require.NotEmpty(t, blockStopTimes)

	indices := []int{0}
	if len(blockStopTimes) > 1 {
		indices = append(indices, len(blockStopTimes)-1)
	}
	for _, idx := range indices {
		st := blockStopTimes[idx]
		assert.GreaterOrEqual(t, st.DistanceAlongBlock, 0.0)
		assert.Contains(t, st.StopTime.StopID, "_", "stop ID should be combined with agency prefix")
		assert.GreaterOrEqual(t, st.StopTime.PickupType, 0, "pickupType should be present and non-negative")
		assert.GreaterOrEqual(t, st.StopTime.DropOffType, 0, "dropOffType should be present and non-negative")
	}

	if len(blockStopTimes) >= 2 {
		first := blockStopTimes[0]
		last := blockStopTimes[len(blockStopTimes)-1]
		assert.Less(t, first.BlockSequence, last.BlockSequence, "blockSequence should increase")
		assert.LessOrEqual(t, first.DistanceAlongBlock, last.DistanceAlongBlock, "distanceAlongBlock should increase")
	}
}

func TestBlockHandlerNonExistentBlock(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[BlockEntryResponse](t, api, blockURL("25_nonexistent"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Equal(t, models.APIVersion, model.Version)
	assert.Greater(t, model.CurrentTime, int64(0))
}

func TestBlockHandlerInvalidBlockID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	testCases := []struct {
		name           string
		endpoint       string
		expectedStatus int
	}{
		{"Empty block ID", blockURL(""), http.StatusBadRequest},
		{"Missing agency separator", blockURL("invalidblock"), http.StatusBadRequest},
		{"Disallowed characters in code ID", blockURL("25_@%23$"), http.StatusBadRequest},
		{"Only underscore", blockURL("_"), http.StatusBadRequest},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, model := callAPIHandler[BlockEntryResponse](t, api, tc.endpoint)

			assert.Equal(t, tc.expectedStatus, resp.StatusCode,
				"Expected HTTP %d for test case: %s", tc.expectedStatus, tc.name)

			assert.Equal(t, tc.expectedStatus, model.Code, "Response model should match expected status code")
			assert.NotEmpty(t, model.Text, "Response model should contain an error message")
			assert.Equal(t, models.APIVersion, model.Version, "Response model should contain API version")
		})
	}
}

func TestBlockHandlerReferencesConsistency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[BlockEntryResponse](t, api, blockURL("25_1"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	entry := model.Data.Entry
	require.NotEmpty(t, entry.Configurations)
	require.NotEmpty(t, entry.Configurations[0].Trips)
	blockStopTimes := entry.Configurations[0].Trips[0].BlockStopTimes
	require.NotEmpty(t, blockStopTimes)
	stopID := blockStopTimes[0].StopTime.StopID

	refStopIDs := make(map[string]bool, len(model.Data.References.Stops))
	for _, s := range model.Data.References.Stops {
		refStopIDs[s.ID] = true
	}
	assert.True(t, refStopIDs[stopID], "Stop %s should be in references", stopID)
}

func TestBlockHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[BlockEntryResponse](t, api, "/api/where/block/25_1.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestBlockHandlerMissingApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[BlockEntryResponse](t, api, "/api/where/block/25_1.json")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestBlockHandlerContextCancellation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req, err := http.NewRequest("GET", blockURL("25_1"), nil)
	require.NoError(t, err)
	// Use a deadline in the past — context.Err() is DeadlineExceeded immediately,
	// no timer resolution dependency (avoids Windows ~15ms minimum sleep issue).
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}

func TestBlockHandlerCrossConfigurationDistanceReset(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[BlockEntryResponse](t, api, blockURL("25_1"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	entry := model.Data.Entry
	require.GreaterOrEqual(t, len(entry.Configurations), 2, "expected at least 2 configurations")

	config0 := entry.Configurations[0]
	require.NotEmpty(t, config0.Trips)
	require.GreaterOrEqual(t, len(config0.Trips[0].BlockStopTimes), 2, "expected at least 2 BlockStopTimes for config0")
	assert.Equal(t, 0.0, config0.Trips[0].BlockStopTimes[0].DistanceAlongBlock, "Configuration 0 first stop should be 0")
	assert.Greater(t, config0.Trips[0].BlockStopTimes[1].DistanceAlongBlock, 0.0, "Configuration 0 second stop should be > 0")

	config1 := entry.Configurations[1]
	require.NotEmpty(t, config1.Trips)
	require.GreaterOrEqual(t, len(config1.Trips[0].BlockStopTimes), 2, "expected at least 2 BlockStopTimes for config1")
	// On the merge base, this incorrectly returned 433750.77 instead of 0.0
	assert.Equal(t, 0.0, config1.Trips[0].BlockStopTimes[0].DistanceAlongBlock, "Configuration 1 first stop should be 0")
	assert.Greater(t, config1.Trips[0].BlockStopTimes[1].DistanceAlongBlock, 0.0, "Configuration 1 second stop should be > 0")
}

func TestBlockHandlerChronologicalTripOrdering(t *testing.T) {
	const agencyID = "test-agency"

	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			agencyID + ",Test Agency,http://example.com,America/Los_Angeles\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"r1," + agencyID + ",1,Route 1,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"s1,1,1,1,1,1,1,1,20250101,20251231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"stop1,Stop 1,40.0,-122.0\n" +
			"stop2,Stop 2,40.1,-122.0\n" +
			"stop3,Stop 3,40.2,-122.0\n",
		"trips.txt": "trip_id,route_id,service_id,block_id\n" +
			"trip_Z_morning,r1,s1,b1\n" +
			"trip_M_midday,r1,s1,b1\n" +
			"trip_A_evening,r1,s1,b1\n" +
			"trip_X_night,r1,s1,b1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			// 1. Morning trip (08:00 - 08:30)
			"trip_Z_morning,08:00:00,08:00:00,stop1,1\n" +
			"trip_Z_morning,08:30:00,08:30:00,stop2,2\n" +
			// 2. Midday trip (12:00 - 12:30)
			"trip_M_midday,12:00:00,12:00:00,stop2,1\n" +
			"trip_M_midday,12:30:00,12:30:00,stop3,2\n" +
			// 3. Evening trip (17:00 - 17:30)
			"trip_A_evening,17:00:00,17:00:00,stop3,1\n" +
			"trip_A_evening,17:30:00,17:30:00,stop1,2\n" +
			// 4. Post-midnight trip (25:30 - 26:00 = 01:30 - 02:00 next day)
			"trip_X_night,25:30:00,25:30:00,stop1,1\n" +
			"trip_X_night,26:00:00,26:00:00,stop2,2\n",
	}

	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "test_block_order.zip", files)

	blockCombinedID := utils.FormCombinedID(agencyID, "b1")
	resp, model := callAPIHandler[BlockEntryResponse](t, api, "/api/where/block/"+blockCombinedID+".json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.Entry.Configurations)
	config := model.Data.Entry.Configurations[0]
	require.Len(t, config.Trips, 4)

	// Explicitly assert operational trip sequence instead of alphabetical ID order:
	expectedTripIDs := []string{
		utils.FormCombinedID(agencyID, "trip_Z_morning"),
		utils.FormCombinedID(agencyID, "trip_M_midday"),
		utils.FormCombinedID(agencyID, "trip_A_evening"),
		utils.FormCombinedID(agencyID, "trip_X_night"),
	}
	actualTripIDs := make([]string, len(config.Trips))
	for i, tr := range config.Trips {
		actualTripIDs[i] = tr.TripId
	}
	assert.Equal(t, expectedTripIDs, actualTripIDs, "Trips must be ordered chronologically by scheduled time, not lexicographically by trip ID")

	// Verify chronological monotonicity (departure of trip[i-1] <= arrival of trip[i]):
	for i := 1; i < len(config.Trips); i++ {
		prevTrip := config.Trips[i-1]
		currTrip := config.Trips[i]
		prevDeparture := prevTrip.BlockStopTimes[len(prevTrip.BlockStopTimes)-1].StopTime.DepartureTime.Duration
		currArrival := currTrip.BlockStopTimes[0].StopTime.ArrivalTime.Duration

		assert.LessOrEqual(t, prevDeparture, currArrival,
			"Chronological violation: Trip %d (%s ends at %v) must not end after Trip %d (%s starts at %v)",
			i-1, prevTrip.TripId, prevDeparture, i, currTrip.TripId, currArrival)
	}

	// Verify DistanceAlongBlock starts at 0 for the first trip and is monotonically non-decreasing across consecutive trips:
	assert.Equal(t, 0.0, config.Trips[0].BlockStopTimes[0].DistanceAlongBlock, "First stop of first trip in block must start at DistanceAlongBlock 0.0")
	for i := 1; i < len(config.Trips); i++ {
		prevTrip := config.Trips[i-1]
		currTrip := config.Trips[i]
		prevEndDist := prevTrip.BlockStopTimes[len(prevTrip.BlockStopTimes)-1].DistanceAlongBlock
		currStartDist := currTrip.BlockStopTimes[0].DistanceAlongBlock

		assert.GreaterOrEqual(t, currStartDist, prevEndDist,
			"DistanceAlongBlock violation: Trip %d starts at %.1f m, which is before previous trip ended at %.1f m",
			i, currStartDist, prevEndDist)
	}
}

func BenchmarkBlockHandler(b *testing.B) {
	api := createTestApi(b)
	defer api.Shutdown()
	endpoint := blockURL("25_1")

	for b.Loop() {
		_, _ = callAPIHandler[BlockEntryResponse](b, api, endpoint)
	}
}
