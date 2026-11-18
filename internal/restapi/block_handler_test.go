package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

type blockSequenceFixtureTrip struct {
	id            string
	stopSequences []int
}

func blockSequenceFixtureFiles(trips []blockSequenceFixtureTrip) map[string]string {
	var tripRows, stopRows, timeRows strings.Builder
	tripRows.WriteString("route_id,service_id,trip_id,block_id\n")
	stopRows.WriteString("stop_id,stop_name,stop_lat,stop_lon\n")
	timeRows.WriteString("trip_id,arrival_time,departure_time,stop_id,stop_sequence\n")

	stopNumber := 0
	for tripIndex, trip := range trips {
		fmt.Fprintf(&tripRows, "route,service,%s,block\n", trip.id)
		for stopIndex, sequence := range trip.stopSequences {
			stopID := fmt.Sprintf("stop-%d", stopNumber)
			stopNumber++
			fmt.Fprintf(&stopRows, "%s,Stop %s,47.0,-122.0\n", stopID, stopID)
			hour := 8 + tripIndex + stopIndex
			fmt.Fprintf(&timeRows, "%s,%02d:00:00,%02d:00:00,%s,%d\n",
				trip.id, hour, hour, stopID, sequence)
		}
	}

	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"block-sequence-agency,Block Sequence,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_type\n" +
			"route,block-sequence-agency,BS,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"service,1,1,1,1,1,1,1,20200101,20301231\n",
		"trips.txt":      tripRows.String(),
		"stops.txt":      stopRows.String(),
		"stop_times.txt": timeRows.String(),
	}
}

func TestBlockHandlerBlockSequenceUsesPositionInBlock(t *testing.T) {
	tests := []struct {
		name        string
		trips       []blockSequenceFixtureTrip
		wantIndices [][]int
	}{
		{
			name:        "zero-based stop numbering",
			trips:       []blockSequenceFixtureTrip{{id: "trip-a", stopSequences: []int{0, 1, 2}}},
			wantIndices: [][]int{{0, 1, 2}},
		},
		{
			name:        "one-based stop numbering",
			trips:       []blockSequenceFixtureTrip{{id: "trip-a", stopSequences: []int{1, 2, 3}}},
			wantIndices: [][]int{{0, 1, 2}},
		},
		{
			name:        "sparse stop numbering",
			trips:       []blockSequenceFixtureTrip{{id: "trip-a", stopSequences: []int{10, 20, 30}}},
			wantIndices: [][]int{{0, 1, 2}},
		},
		{
			name: "sequence continues across trips",
			trips: []blockSequenceFixtureTrip{
				{id: "trip-a", stopSequences: []int{10, 20}},
				{id: "trip-b", stopSequences: []int{10, 20}},
			},
			wantIndices: [][]int{{0, 1}, {2, 3}},
		},
		{
			name: "trips follow run order rather than trip ID",
			trips: []blockSequenceFixtureTrip{
				{id: "T2", stopSequences: []int{10, 20}},
				{id: "T10", stopSequences: []int{10, 20}},
			},
			wantIndices: [][]int{{0, 1}, {2, 3}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "block-sequence.zip", blockSequenceFixtureFiles(tt.trips))
			defer api.Shutdown()

			_, model := callAPIHandler[BlockEntryResponse](t, api, blockURL("block-sequence-agency_block"))

			require.Len(t, model.Data.Entry.Configurations, 1)
			blockTrips := model.Data.Entry.Configurations[0].Trips
			require.Len(t, blockTrips, len(tt.wantIndices))
			for i, trip := range tt.trips {
				assert.Equal(t, "block-sequence-agency_"+trip.id, blockTrips[i].TripId)
			}
			for tripIndex, want := range tt.wantIndices {
				stopTimes := blockTrips[tripIndex].BlockStopTimes
				require.Len(t, stopTimes, len(want))
				got := make([]int, len(stopTimes))
				for i, stopTime := range stopTimes {
					got[i] = stopTime.BlockSequence
				}
				assert.Equal(t, want, got)
			}
		})
	}
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

func BenchmarkBlockHandler(b *testing.B) {
	api := createTestApi(b)
	defer api.Shutdown()
	endpoint := blockURL("25_1")

	for b.Loop() {
		_, _ = callAPIHandler[BlockEntryResponse](b, api, endpoint)
	}
}

func TestBlockHandlerBlocklessTrip(t *testing.T) {
	files := basicTripsForRouteFiles()
	// Trip with no block_id (blockless trip) alongside a blocked trip.
	files["trips.txt"] = "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
		tripsForRouteRouteID + ",tfr-svc," + tripsForRouteTripID + "," + tripsForRouteHeadsign + ",0,\n" +
		tripsForRouteRouteID + ",tfr-svc,blocked-trip," + tripsForRouteHeadsign + ",0,real-block\n"
	files["stop_times.txt"] = "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
		tripsForRouteTripID + ",11:55:00,11:55:00," + tripsForRouteStop1ID + ",1\n" +
		tripsForRouteTripID + ",12:05:00,12:05:00," + tripsForRouteStop2ID + ",2\n" +
		"blocked-trip,12:15:00,12:15:00," + tripsForRouteStop1ID + ",1\n" +
		"blocked-trip,12:25:00,12:25:00," + tripsForRouteStop2ID + ",2\n"

	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "blockless-block.zip", files)
	defer api.Shutdown()

	combinedBlocklessBlockID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteTripID)

	// 1. Querying block endpoint with blockless trip ID returns 200 with single-trip configuration.
	resp, model := callAPIHandler[BlockEntryResponse](t, api, blockURL(combinedBlocklessBlockID))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, combinedBlocklessBlockID, model.Data.Entry.ID)
	require.Len(t, model.Data.Entry.Configurations, 1)

	config := model.Data.Entry.Configurations[0]
	assert.Equal(t, []string{utils.FormCombinedID(tripsForRouteAgencyID, "tfr-svc")}, config.ActiveServiceIds)
	require.Len(t, config.Trips, 1)

	trip := config.Trips[0]
	assert.Equal(t, combinedBlocklessBlockID, trip.TripId)
	require.Len(t, trip.BlockStopTimes, 2)
	assert.Equal(t, 0, trip.BlockStopTimes[0].BlockSequence)
	assert.Equal(t, 1, trip.BlockStopTimes[1].BlockSequence)
	assert.Equal(t, utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteStop1ID), trip.BlockStopTimes[0].StopTime.StopID)
	assert.Equal(t, utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteStop2ID), trip.BlockStopTimes[1].StopTime.StopID)

	// Verify references contain the trip with its synthetic blockId matching combinedBlocklessBlockID.
	require.Len(t, model.Data.References.Trips, 1)
	assert.Equal(t, combinedBlocklessBlockID, model.Data.References.Trips[0].ID)
	assert.Equal(t, combinedBlocklessBlockID, model.Data.References.Trips[0].BlockID)

	// 2. Querying block endpoint with a blocked trip ID returns 404.
	blockedTripAsBlockID := utils.FormCombinedID(tripsForRouteAgencyID, "blocked-trip")
	respBlocked, modelBlocked := callAPIHandler[BlockEntryResponse](t, api, blockURL(blockedTripAsBlockID))
	assert.Equal(t, http.StatusNotFound, respBlocked.StatusCode)
	assert.Equal(t, http.StatusNotFound, modelBlocked.Code)

	// 3. Querying block endpoint with the real block ID works as expected.
	realBlockID := utils.FormCombinedID(tripsForRouteAgencyID, "real-block")
	respReal, modelReal := callAPIHandler[BlockEntryResponse](t, api, blockURL(realBlockID))
	assert.Equal(t, http.StatusOK, respReal.StatusCode)
	assert.Equal(t, http.StatusOK, modelReal.Code)
	assert.Equal(t, realBlockID, modelReal.Data.Entry.ID)
	require.Len(t, modelReal.Data.Entry.Configurations, 1)
	require.Len(t, modelReal.Data.Entry.Configurations[0].Trips, 1)
	assert.Equal(t, blockedTripAsBlockID, modelReal.Data.Entry.Configurations[0].Trips[0].TripId)

	// 4. Querying block endpoint for blockless trip with different agency returns 404.
	otherAgencyBlockID := utils.FormCombinedID("other-agency", tripsForRouteTripID)
	respOther, modelOther := callAPIHandler[BlockEntryResponse](t, api, blockURL(otherAgencyBlockID))
	assert.Equal(t, http.StatusNotFound, respOther.StatusCode)
	assert.Equal(t, http.StatusNotFound, modelOther.Code)
}
