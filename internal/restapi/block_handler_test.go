package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
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

	// Verify TimeZone is populated with the correct agency timezone
	assert.Equal(t, testdata.Raba.Timezone, config.TimeZone, "timeZone should match the agency's timezone")

	require.NotEmpty(t, config.Trips)

	trip := config.Trips[0]
	assert.Contains(t, trip.TripId, "_", "trip ID should be combined with agency prefix")
	assert.NotZero(t, trip.DistanceAlongBlock)
	assert.NotEmpty(t, trip.BlockStopTimes)

	// Iterate every config/trip/stop-time — catches empty IDs in less-common configurations.
	for _, c := range entry.Configurations {
		assert.NotEmpty(t, c.ActiveServiceIds)
		assert.NotEmpty(t, c.TimeZone, "timeZone should be populated for all configurations")
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

func BenchmarkBlockHandler(b *testing.B) {
	api := createTestApi(b)
	defer api.Shutdown()
	endpoint := blockURL("25_1")

	for b.Loop() {
		_, _ = callAPIHandler[BlockEntryResponse](b, api, endpoint)
	}
}

func TestTransformBlockToEntry_GroupingAndSorting(t *testing.T) {
	agencyID := "TEST"
	blockID := "B1"
	timezone := "America/Los_Angeles"

	// Helper to quickly mock DB rows
	makeRow := func(serviceID, tripID string, stopSeq int64) gtfsdb.GetBlockDetailsRow {
		return gtfsdb.GetBlockDetailsRow{
			ServiceID:    serviceID,
			TripID:       tripID,
			StopSequence: stopSeq,
			StopID:       "S" + strconv.Itoa(int(stopSeq)), // Dummy stop ID
		}
	}

	mockDBRows := []gtfsdb.GetBlockDetailsRow{
		// S_B has a unique itinerary. (Group of 1)
		makeRow("S_B", "T2", 1),

		// S_A, S_C, and S_D share the exact same itinerary (T1 with 2 stops). (Group of 3)
		makeRow("S_C", "T1", 1), makeRow("S_C", "T1", 2),
		makeRow("S_A", "T1", 1), makeRow("S_A", "T1", 2),
		makeRow("S_D", "T1", 1), makeRow("S_D", "T1", 2),

		// S_E and S_F share another identical itinerary. (Group of 2)
		makeRow("S_E", "T3", 1),
		makeRow("S_F", "T3", 1),
	}

	// Execute our new grouping and sorting logic
	entry := transformBlockToEntry(mockDBRows, blockID, agencyID, timezone)

	// ASSERTIONS
	require.Len(t, entry.Configurations, 3, "Should compress 6 service calendars into 3 distinct configurations")

	// 1st config: Largest group (3 items). Ties broken by natural alphabetical sort.
	assert.Equal(t, []string{"TEST_S_A", "TEST_S_C", "TEST_S_D"}, entry.Configurations[0].ActiveServiceIds)
	assert.Equal(t, timezone, entry.Configurations[0].TimeZone)

	// 2nd config: Next largest group (2 items).
	assert.Equal(t, []string{"TEST_S_E", "TEST_S_F"}, entry.Configurations[1].ActiveServiceIds)

	// 3rd config: Smallest group (1 item).
	assert.Equal(t, []string{"TEST_S_B"}, entry.Configurations[2].ActiveServiceIds)
}
