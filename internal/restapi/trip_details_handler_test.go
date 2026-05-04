package restapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func TestTripDetailsHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/invalid.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestTripDetailsHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	loc, err := time.LoadLocation(agency.Timezone)
	require.NoError(t, err)
	now := time.Now().In(loc)
	y, m, d := now.Date()
	expectedServiceDate := time.Date(y, m, d, 0, 0, 0, 0, loc)

	resp, model := callAPIHandler[TripDetailsResponse](t, api, "/api/where/trip-details/"+tripID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	entry := model.Data.Entry
	assert.Equal(t, tripID, entry.TripID)
	assert.Equal(t, expectedServiceDate.UnixMilli(), entry.ServiceDate.UnixMilli())

	assert.NotNil(t, entry.SituationIDs)

	require.NotNil(t, entry.Schedule)
	assert.NotEmpty(t, entry.Schedule.TimeZone)
	if len(entry.Schedule.StopTimes) > 0 {
		st := entry.Schedule.StopTimes[0]
		assert.NotEmpty(t, st.StopID)
		assert.NotZero(t, st.ArrivalTime)
		assert.NotZero(t, st.DepartureTime)
	}

	if entry.Status != nil {
		assert.NotZero(t, entry.Status.ServiceDate)
		assert.Contains(t, []string{"scheduled", "in_progress", "completed"}, entry.Status.Phase)
		assert.NotNil(t, entry.Status.Predicted)
	}

	refs := model.Data.References
	require.NotEmpty(t, refs.Trips)
	assert.Equal(t, tripID, refs.Trips[0].ID)
	assert.Equal(t, utils.FormCombinedID(agency.ID, trip.RouteID), refs.Trips[0].RouteID)
	assert.Equal(t, utils.FormCombinedID(agency.ID, trip.ServiceID), refs.Trips[0].ServiceID)

	require.NotEmpty(t, refs.Routes)
	require.NotEmpty(t, refs.Agencies)
	assert.Equal(t, agency.ID, refs.Agencies[0].ID)
	assert.Equal(t, agency.Name, refs.Agencies[0].Name)

	if len(refs.Stops) > 0 {
		stop := refs.Stops[0]
		assert.NotEmpty(t, stop.ID)
		assert.NotEmpty(t, stop.Name)
		assert.NotZero(t, stop.Lat)
		assert.NotZero(t, stop.Lon)
	}
}

func TestTripDetailsHandlerWithInvalidTripID(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/agency_invalid.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestTripDetailsHandlerWithServiceDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	tomorrow := time.Now().AddDate(0, 0, 1)
	serviceDateMs := tomorrow.Unix() * 1000
	// serviceDate in response is midnight in the agency's timezone, not the raw input epoch.
	agencyLoc, _ := time.LoadLocation("America/Los_Angeles")
	sdInAgencyTz := tomorrow.In(agencyLoc)
	expectedMidnight := time.Date(sdInAgencyTz.Year(), sdInAgencyTz.Month(), sdInAgencyTz.Day(),
		0, 0, 0, 0, agencyLoc)

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-details/"+tripID+".json?key=TEST&serviceDate="+
			strconv.FormatInt(serviceDateMs, 10))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, expectedMidnight.UnixMilli(), model.Data.Entry.ServiceDate.UnixMilli())
}

func TestTripDetailsHandlerWithIncludeTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-details/"+tripID+".json?key=TEST&includeTrip=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.References.Trips)
}

func TestTripDetailsHandlerWithIncludeSchedule(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-details/"+tripID+".json?key=TEST&includeSchedule=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, model.Data.Entry.Schedule)
	assert.Empty(t, model.Data.References.Stops)
}

func TestTripDetailsHandlerWithIncludeStatus(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-details/"+tripID+".json?key=TEST&includeStatus=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, model.Data.Entry.Status)
}

func TestTripDetailsHandlerWithTimeParameter(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	timeMs := time.Now().Add(1 * time.Hour).Unix() * 1000

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-details/"+tripID+".json?key=TEST&time="+strconv.FormatInt(timeMs, 10))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.NotEmpty(t, model.Data.Entry.TripID)
}

func TestTripDetailsHandlerWithAllParametersFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-details/"+tripID+".json?key=TEST&includeTrip=false&includeSchedule=false&includeStatus=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	entry := model.Data.Entry
	assert.Equal(t, tripID, entry.TripID)
	assert.NotZero(t, entry.ServiceDate.UnixMilli())
	assert.Nil(t, entry.Schedule)
	assert.Nil(t, entry.Status)

	assert.Empty(t, model.Data.References.Routes)
	assert.NotEmpty(t, model.Data.References.Agencies)
}

func TestTripDetailsHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[TripDetailsResponse](t, api, "/api/where/trip-details/1110.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripDetailsHandlerWithInvalidParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)

	t.Run("invalid serviceDate", func(t *testing.T) {
		resp, _ := callAPIHandler[TripDetailsResponse](t, api,
			"/api/where/trip-details/"+tripID+".json?key=TEST&serviceDate=invalid")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("invalid time", func(t *testing.T) {
		resp, _ := callAPIHandler[TripDetailsResponse](t, api,
			"/api/where/trip-details/"+tripID+".json?key=TEST&time=invalid")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestParseTripIdDetailsParams_Unit(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	t.Run("explicit params", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/?includeTrip=false&includeSchedule=false&serviceDate=1609459200000", nil)

		params, errs := api.parseTripParams(req, true)

		assert.Nil(t, errs)
		assert.False(t, params.IncludeTrip)
		assert.False(t, params.IncludeSchedule)
		assert.NotNil(t, params.ServiceDate)
	})

	t.Run("defaults", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)

		params, errs := api.parseTripParams(req, true)

		assert.Nil(t, errs)
		assert.True(t, params.IncludeTrip)
		assert.True(t, params.IncludeStatus)
		assert.True(t, params.IncludeSchedule)
	})

	t.Run("invalid params return field errors", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/?time=invalid&serviceDate=invalid", nil)

		_, errs := api.parseTripParams(req, true)

		assert.NotNil(t, errs)
		assert.Contains(t, errs, "time")
		assert.Contains(t, errs, "serviceDate")
		assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errs["time"][0])
	})
}
