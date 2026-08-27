package restapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

func stopsForTripURL(tripID string) string {
	return "/api/where/stops-for-trip/" + tripID + ".json?key=TEST"
}

func TestStopsForTripRequiresValidAPIKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsForTripResponse](t, api,
		"/api/where/stops-for-trip/"+utils.FormCombinedID(testdata.Raba.ID, "anything")+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
}

func TestStopsForTripNotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsForTripResponse](t, api,
		stopsForTripURL(utils.FormCombinedID(testdata.Raba.ID, "nonexistent-trip")))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

func TestStopsForTripEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	combinedTripID := utils.FormCombinedID(testdata.Raba.ID, trip.ID)

	resp, model := callAPIHandler[StopsForTripResponse](t, api, stopsForTripURL(combinedTripID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	require.NotEmpty(t, model.Data.List, "trip should have at least one stop")
	assert.False(t, model.Data.LimitExceeded)

	for i, stop := range model.Data.List {
		assert.NotEmpty(t, stop.ID, "stop[%d].ID should not be empty", i)
		assert.NotZero(t, stop.Lat, "stop[%d].Lat should not be zero", i)
		assert.NotZero(t, stop.Lon, "stop[%d].Lon should not be zero", i)
		assert.True(t, strings.HasPrefix(stop.ID, testdata.Raba.ID+"_"),
			"stop[%d].ID %q should have agency prefix", i, stop.ID)
	}

	assert.NotEmpty(t, model.Data.References.Agencies)
	assert.NotEmpty(t, model.Data.References.Routes)
}

func TestStopsForTripOrder(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	combinedTripID := utils.FormCombinedID(testdata.Raba.ID, trip.ID)

	expectedStopIDs, err := api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(
		context.Background(), trip.ID)
	require.NoError(t, err)
	require.NotEmpty(t, expectedStopIDs)

	_, model := callAPIHandler[StopsForTripResponse](t, api, stopsForTripURL(combinedTripID))

	require.Len(t, model.Data.List, len(expectedStopIDs))
	for i, stop := range model.Data.List {
		wantID := utils.FormCombinedID(testdata.Raba.ID, expectedStopIDs[i])
		assert.Equal(t, wantID, stop.ID, "stop[%d] should match ordered stop sequence", i)
	}
}

func TestStopsForTripIncludeReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	combinedTripID := utils.FormCombinedID(testdata.Raba.ID, trip.ID)

	t.Run("includeReferences=false", func(t *testing.T) {
		_, model := callAPIHandler[StopsForTripResponse](t, api,
			stopsForTripURL(combinedTripID)+"&includeReferences=false")

		assert.Empty(t, model.Data.References.Agencies)
		assert.Empty(t, model.Data.References.Routes)
		assert.Empty(t, model.Data.References.Stops)
		assert.NotEmpty(t, model.Data.List, "stops should still be present")
	})

	t.Run("includeReferences=true", func(t *testing.T) {
		_, model := callAPIHandler[StopsForTripResponse](t, api,
			stopsForTripURL(combinedTripID)+"&includeReferences=true")

		assert.NotEmpty(t, model.Data.References.Agencies)
		assert.NotEmpty(t, model.Data.References.Routes)
	})
}
