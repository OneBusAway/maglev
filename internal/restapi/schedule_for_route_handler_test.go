package restapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// scheduleForRouteFixedClock is the mock-clock instant used across schedule-for-route
// tests; 2025-06-12 12:00 UTC corresponds to a known service date in the RABA test data.
var scheduleForRouteFixedClock = time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC)

func newScheduleForRouteAPI(t *testing.T) *RestAPI {
	t.Helper()
	return createTestApiWithClock(t, clock.NewMockClock(scheduleForRouteFixedClock))
}

func scheduleForRouteURL(routeID, date string) string {
	u := "/api/where/schedule-for-route/" + routeID + ".json?key=TEST"
	if date != "" {
		u += "&date=" + date
	}
	return u
}

func assertScheduleOK(t *testing.T, resp *http.Response, model ScheduleForRouteResponse) {
	t.Helper()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
}

func assertScheduleErr(t *testing.T, resp *http.Response, model ScheduleForRouteResponse, wantCode int, wantText string) {
	t.Helper()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, wantCode, model.Code)
	assert.Equal(t, wantText, model.Text)
}

func agencyDate(t *testing.T, ymd string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(testdata.Raba.Timezone)
	require.NoError(t, err)
	d, err := time.ParseInLocation("2006-01-02", ymd, loc)
	require.NoError(t, err)
	return d
}

func TestScheduleForRouteHandler(t *testing.T) {
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID

	t.Run("Valid route", func(t *testing.T) {
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2025-06-12"))

		assertScheduleOK(t, resp, model)

		expectedScheduleDate := agencyDate(t, "2025-06-12")

		entry := model.Data.Entry
		assert.Equal(t, routeID, entry.RouteID)
		assert.Equal(t, expectedScheduleDate.UnixMilli(), entry.ScheduleDate)

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
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID+"notexist", ""))
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, http.StatusNotFound, model.Code)
	})
}

func TestScheduleForRouteHandlerDateParam(t *testing.T) {
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID

	t.Run("No date param uses current date", func(t *testing.T) {
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, ""))

		assertScheduleOK(t, resp, model)

		// Clock is 2025-06-12 12:00 UTC = 2025-06-12 05:00 PDT, so start of day in LA is 2025-06-12.
		expectedScheduleDate := agencyDate(t, "2025-06-12")
		assert.Equal(t, expectedScheduleDate.UnixMilli(), model.Data.Entry.ScheduleDate)
	})

	t.Run("Invalid date format returns 400 with fieldErrors", func(t *testing.T) {
		resp, body := callAPIHandler[map[string]any](t, api, scheduleForRouteURL(routeID, "2025/06/12"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		fieldErrors, ok := body["fieldErrors"].(map[string]any)
		require.True(t, ok, "response should have a fieldErrors object")
		assert.Contains(t, fieldErrors, "date", "fieldErrors should contain a 'date' key")
	})

	t.Run("Epoch ms date parsed as Java OBA compatibility", func(t *testing.T) {
		// date=0 → epoch start (1970-01-01 00:00:00 UTC) → before any RABA service → NoServiceThatDay
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "0"))
		assertScheduleErr(t, resp, model, 510, "NoServiceThatDay")
	})

	t.Run("Epoch ms for valid service date returns schedule", func(t *testing.T) {
		// 1749711600000 = 2025-06-12 00:00:00 PDT (America/Los_Angeles), which has RABA service.
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "1749711600000"))

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.NotEqual(t, "NoServiceThatDay", model.Text)
		assert.NotEmpty(t, model.Data.Entry.StopTripGroupings)
	})
}

// Regression for #790: serviceIds must be derived from the route's actual
// trips, not from the agency's active service IDs for the day. Route 25_1885
// uses only c_868_b_79978_d_31, while several other services are active on
// the same weekday — the response must include only the route-scoped set.
func TestScheduleForRouteHandler_ServiceIDsScopedToRoute(t *testing.T) {
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := utils.FormCombinedID("25", "1885")
	expectedServiceID := utils.FormCombinedID("25", "c_868_b_79978_d_31")

	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2025-06-12"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.ElementsMatch(t, []string{expectedServiceID}, model.Data.Entry.ServiceIDs,
		"serviceIds must be scoped to the route's trips, not agency-wide active services")
}

func TestScheduleForRouteHandler_DirectionIDMatchesCSV(t *testing.T) {
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := utils.FormCombinedID("25", "1885")
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2025-06-12"))
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
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2025-06-12"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.References.Agencies)
	require.NotEmpty(t, model.Data.References.Routes)
	require.NotEmpty(t, model.Data.References.Trips)
	require.NotEmpty(t, model.Data.References.Stops)
	require.NotEmpty(t, model.Data.References.StopTimes)
}

func TestScheduleForRouteHandler_TripIDsSorted(t *testing.T) {
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2025-06-12"))

	require.Equal(t, http.StatusOK, resp.StatusCode)

	for _, grouping := range model.Data.Entry.StopTripGroupings {
		assert.IsNonDecreasing(t, grouping.TripIDs, "tripIDs should be sorted lexicographically")
	}
}

func TestScheduleForRouteHandler_ServiceDateOutOfRange(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID

	t.Run("Future date beyond feed returns ServiceDateOutOfRange", func(t *testing.T) {
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2099-01-01"))
		assertScheduleErr(t, resp, model, 510, "ServiceDateOutOfRange")
		assert.Empty(t, model.Data.Entry.RouteID, "data.entry should be absent for ServiceDateOutOfRange")
	})

	t.Run("Garbage date string returns 400 with fieldErrors", func(t *testing.T) {
		resp, body := callAPIHandler[map[string]any](t, api, scheduleForRouteURL(routeID, "not-a-date"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		fieldErrors, ok := body["fieldErrors"].(map[string]any)
		require.True(t, ok, "response should have a fieldErrors object")
		assert.Contains(t, fieldErrors, "date", "fieldErrors should contain a 'date' key")
	})
}

func TestScheduleForRouteHandler_NoServiceThatDay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID

	t.Run("Early date before feed returns NoServiceThatDay with references", func(t *testing.T) {
		// 1970-01-01 is before any RABA calendar data but not after the feed end date.
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "1970-01-01"))

		assertScheduleErr(t, resp, model, 510, "NoServiceThatDay")

		entry := model.Data.Entry
		assert.NotEmpty(t, entry.RouteID, "routeId should be present in NoServiceThatDay")
		assert.Empty(t, entry.ServiceIDs)
		assert.Empty(t, entry.StopTripGroupings)

		refs := model.Data.References
		assert.NotEmpty(t, refs.Agencies, "references.agencies should be populated for NoServiceThatDay")
		assert.NotEmpty(t, refs.Routes, "references.routes should be populated for NoServiceThatDay")
	})
}

func TestScheduleForRouteHandler_TripReferenceCombinedIDs(t *testing.T) {
	api := newScheduleForRouteAPI(t)
	defer api.Shutdown()

	routeID := testdata.Route1.ID
	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL(routeID, "2025-06-12"))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.References.Trips)

	for _, tr := range model.Data.References.Trips {
		assert.Contains(t, tr.RouteID, "_", "trip reference routeId must be a combined ID")
		assert.Contains(t, tr.ServiceID, "_", "trip reference serviceId must be a combined ID")
	}
}

func TestScheduleForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, scheduleForRouteURL("1110", ""))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
	assert.Equal(t, http.StatusBadRequest, model.Code)
}
