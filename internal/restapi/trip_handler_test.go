package restapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// tripURL builds the /trip endpoint URL with key=TEST baked in. Tests that
// want a different key (auth checks) build their URL inline.
func tripURL(tripID string) string {
	return "/api/where/trip/" + tripID + ".json?key=TEST"
}

func TestTripHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[TripEntryResponse](t, api,
		"/api/where/trip/invalid.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestTripHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	combinedTripID := utils.FormCombinedID(testdata.Raba.ID, trip.ID)

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(context.Background(), trip.RouteID)
	require.NoError(t, err)

	resp, model := callAPIHandler[TripEntryResponse](t, api, tripURL(combinedTripID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	entry := model.Data.Entry
	assert.Equal(t, combinedTripID, entry.ID)
	assert.Equal(t, utils.FormCombinedID(testdata.Raba.ID, trip.RouteID), entry.RouteID)
	assert.Equal(t, utils.FormCombinedID(testdata.Raba.ID, trip.ServiceID), entry.ServiceID)
	assert.Equal(t, fmt.Sprintf("%d", nulls.Int64OrDefault(trip.DirectionID, 0)), entry.DirectionID)
	assert.Equal(t, utils.FormCombinedID(testdata.Raba.ID, nulls.StringOrEmpty(trip.BlockID)), entry.BlockID)
	assert.Equal(t, utils.FormCombinedID(testdata.Raba.ID, nulls.StringOrEmpty(trip.ShapeID)), entry.ShapeID)
	assert.Equal(t, nulls.StringOrEmpty(trip.TripHeadsign), entry.TripHeadsign)
	assert.Equal(t, nulls.StringOrEmpty(trip.TripShortName), entry.TripShortName)
	// entry.routeShortName is the trip's own per-trip route short name, not the
	// route's. Maglev does not store that column, so it is always empty (parity
	// with the Java reference). Clients fall back to the route reference's shortName.
	assert.Equal(t, "", entry.RouteShortName)

	// Route reference: id is combined, agencyId matches, shortName echoes the DB.
	require.NotEmpty(t, model.Data.References.Routes)
	routeRef := model.Data.References.Routes[0]
	assert.Equal(t, utils.FormCombinedID(testdata.Raba.ID, trip.RouteID), routeRef.ID)
	assert.Equal(t, testdata.Raba.ID, routeRef.AgencyID)
	assert.Equal(t, nulls.StringOrEmpty(route.ShortName), routeRef.ShortName)

	// Agency reference: the RABA fixture should be exactly one entry.
	assert.Contains(t, model.Data.References.Agencies, testdata.Raba)
}

func TestTripHandler_IncludeReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	combinedTripID := utils.FormCombinedID(testdata.Raba.ID, trip.ID)

	t.Run("includeReferences=false returns empty references", func(t *testing.T) {
		resp, model := callAPIHandler[TripEntryResponse](t, api,
			tripURL(combinedTripID)+"&includeReferences=false")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)

		// Entry is still fully populated.
		assert.Equal(t, combinedTripID, model.Data.Entry.ID)

		// All references sub-arrays are empty.
		assert.Empty(t, model.Data.References.Agencies)
		assert.Empty(t, model.Data.References.Routes)
		assert.Empty(t, model.Data.References.Stops)
		assert.Empty(t, model.Data.References.Trips)
		assert.Empty(t, model.Data.References.Situations)
	})

	t.Run("includeReferences=true populates references", func(t *testing.T) {
		resp, model := callAPIHandler[TripEntryResponse](t, api,
			tripURL(combinedTripID)+"&includeReferences=true")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, model.Data.References.Routes)
		assert.NotEmpty(t, model.Data.References.Agencies)
	})

	t.Run("absent includeReferences defaults to true", func(t *testing.T) {
		resp, model := callAPIHandler[TripEntryResponse](t, api, tripURL(combinedTripID))

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, model.Data.References.Routes)
		assert.NotEmpty(t, model.Data.References.Agencies)
	})
}

func TestTripHandler_NotFoundAndMalformed(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name           string
		tripID         string
		expectedStatus int
		expectedText   string
	}{
		{
			"Unknown trip code",
			utils.FormCombinedID(testdata.Raba.ID, "invalid"),
			http.StatusNotFound,
			"resource not found",
		},
		{
			"Malformed (no agency separator)",
			"1110",
			http.StatusBadRequest,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[TripEntryResponse](t, api, tripURL(tt.tripID))

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Equal(t, tt.expectedStatus, model.Code)
			if tt.expectedText != "" {
				assert.Equal(t, tt.expectedText, model.Text)
			}
		})
	}
}
