package restapi

import (
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

func TestScheduleForRouteHandler(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	routeID := testdata.Route1.ID

	t.Run("Valid route", func(t *testing.T) {
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, "/api/where/schedule-for-route/"+routeID+".json?key=TEST&date=2025-06-12")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		entry := model.Data.Entry
		assert.Equal(t, routeID, entry.RouteID)
		assert.Greater(t, entry.ScheduleDate, int64(0))

		require.NotEmpty(t, entry.ServiceIDs)

		require.NotEmpty(t, entry.StopTripGroupings)

		firstGrouping := entry.StopTripGroupings[0]
		assert.NotEmpty(t, firstGrouping.DirectionID)
		assert.NotEmpty(t, firstGrouping.TripHeadsigns)
		assert.NotEmpty(t, firstGrouping.StopIDs)
		assert.NotEmpty(t, firstGrouping.TripIDs)
		require.NotEmpty(t, firstGrouping.TripsWithStopTimes)

		firstTripWithStops := firstGrouping.TripsWithStopTimes[0]
		require.Contains(t, firstTripWithStops.TripID, "_", "TripID should be combined with agency prefix")

		require.NotEmpty(t, firstTripWithStops.StopTimes)

		st0 := firstTripWithStops.StopTimes[0]
		assert.True(t, st0.ArrivalEnabled)
		assert.True(t, st0.DepartureEnabled)
		assert.GreaterOrEqual(t, st0.DepartureTime.Duration, st0.ArrivalTime.Duration)

		assert.NotEmpty(t, model.Data.References.StopTimes)
		firstRefST := model.Data.References.StopTimes[0]
		require.Contains(t, firstRefST.TripID, "_", "tripId should be a combined ID")
		require.Contains(t, firstRefST.StopID, "_", "stopId should be a combined ID")
	})

	t.Run("Invalid route", func(t *testing.T) {
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, "/api/where/schedule-for-route/"+routeID+"notexist.json?key=TEST")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, http.StatusNotFound, model.Code)
	})
}

func TestScheduleForRouteHandlerDateParam(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID

	t.Run("Valid date parameter", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		assert.Greater(t, model.Data.Entry.ScheduleDate, int64(0))
	})

	t.Run("Invalid date format", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025/06/12"
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, http.StatusBadRequest, model.Code)
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
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.ElementsMatch(t, []string{expectedServiceID}, model.Data.Entry.ServiceIDs,
		"serviceIds must be scoped to the route's trips, not agency-wide active services")
}

func TestScheduleForRouteHandler_DirectionIDMatchesCSV(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	routeID := utils.FormCombinedID("25", "1885")
	endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	groupings := model.Data.Entry.StopTripGroupings
	require.Len(t, groupings, 2, "route 25_1885 has trips in both directions")

	tripDirByID := make(map[string]string, len(model.Data.References.Trips))
	for _, tr := range model.Data.References.Trips {
		tripDirByID[tr.ID] = tr.DirectionID
	}

	assert.Equal(t, "0", groupings[0].DirectionID)
	assert.Equal(t, "1", groupings[1].DirectionID)

	for _, g := range groupings {
		require.NotEmpty(t, g.TripIDs)
		for _, tid := range g.TripIDs {
			assert.Equal(t, g.DirectionID, tripDirByID[tid],
				"group %q trip %q should have CSV direction_id %q, got %q", g.DirectionID, tid, g.DirectionID, tripDirByID[tid])
		}
	}
}

func TestScheduleForRouteHandler_WithReferences(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	routeID := testdata.Route1.ID
	endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.References.Agencies)
	require.NotEmpty(t, model.Data.References.Routes)
	require.NotEmpty(t, model.Data.References.Trips)
	require.NotEmpty(t, model.Data.References.Stops)
	require.NotEmpty(t, model.Data.References.StopTimes)
}

func TestScheduleForRouteHandler_TripIDsSorted(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	routeID := testdata.Route1.ID
	endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

	require.Equal(t, http.StatusOK, resp.StatusCode)

	for _, grouping := range model.Data.Entry.StopTripGroupings {
		tripIDs := grouping.TripIDs
		if len(tripIDs) < 2 {
			continue
		}
		sortedTripIDs := make([]string, len(tripIDs))
		copy(sortedTripIDs, tripIDs)
		sort.Strings(sortedTripIDs)
		assert.Equal(t, sortedTripIDs, tripIDs, "tripIDs should be sorted lexicographically")
	}
}

func TestScheduleForRouteHandler_NoServiceOrOutOfRange(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID
	futureDate := "2099-01-01"
	endpoint := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=" + futureDate
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Text)
}

func TestScheduleForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/schedule-for-route/" + malformedID + ".json?key=TEST"

	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
	assert.Equal(t, http.StatusBadRequest, model.Code)
}
