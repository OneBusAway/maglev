package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

func TestArrivalAndDepartureForStopHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, "/api/where/arrival-and-departure-for-stop/invalid.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestArrivalAndDepartureForStopHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := utils.FormCombinedID("25", "4062")
	tripID := utils.FormCombinedID("25", "0f36bccf-c435-4b31-b001-da345d06a57d")
	serviceDate := time.Now()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, serviceDate.UnixMilli())
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", model.Text)

	entry := model.Data.Entry
	assert.Equal(t, stopID, entry.StopID)
	assert.Equal(t, tripID, entry.TripID)
	// serviceDate in response is midnight of the service date in the agency's timezone,
	// not the raw input epoch. The RABA agency uses America/Los_Angeles.
	agencyLoc, _ := time.LoadLocation("America/Los_Angeles")
	sdTime := serviceDate.In(agencyLoc)
	expectedMidnight := time.Date(sdTime.Year(), sdTime.Month(), sdTime.Day(), 0, 0, 0, 0, agencyLoc)
	assert.Equal(t, expectedMidnight, entry.ServiceDate.In(agencyLoc))
	assert.False(t, entry.ScheduledArrivalTime.IsZero())
	assert.False(t, entry.ScheduledDepartureTime.IsZero())
	assert.True(t, entry.ArrivalEnabled)
	assert.True(t, entry.DepartureEnabled)
	assert.Equal(t, 16, entry.StopSequence)
	assert.NotZero(t, entry.TotalStopsInTrip)

	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
	assert.Contains(t, model.Data.References.Routes, testdata.Route4)
	assert.Contains(t, model.Data.References.Stops, testdata.Stop4062)
	assert.NotEmpty(t, model.Data.References.Trips)
}

// TestArrivalAndDepartureWithFrequency verifies the ArrivalAndDeparture entry
// carries frequency data when the requested trip is frequency-based, covering
// both exact_times=0 (headway-based) and exact_times=1 (schedule-based)
// windows, and that non-frequency trips keep the field absent.
func TestArrivalAndDepartureWithFrequency(t *testing.T) {
	api := createTestApiWithFrequencyData(t)

	serviceDate := time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		tripID            string
		wantExactTimes    int
		wantHeadway       time.Duration
		wantFrequencyData bool
	}{
		{
			name:              "headway-based trip (exact_times=0)",
			tripID:            freqTripID,
			wantExactTimes:    0,
			wantHeadway:       600 * time.Second,
			wantFrequencyData: true,
		},
		{
			name:              "schedule-based trip (exact_times=1)",
			tripID:            freqExactTripID,
			wantExactTimes:    1,
			wantHeadway:       1800 * time.Second,
			wantFrequencyData: true,
		},
		{
			name:              "non-frequency trip",
			tripID:            freqNormalTripD,
			wantFrequencyData: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopID := utils.FormCombinedID(freqAgencyID, freqStopAID)
			tripID := utils.FormCombinedID(freqAgencyID, tt.tripID)

			endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d",
				stopID, tripID, serviceDate.UnixMilli())
			resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, http.StatusOK, model.Code)

			entry := model.Data.Entry
			if !tt.wantFrequencyData {
				assert.Nil(t, entry.Frequency, "non-frequency trips must not carry frequency data")
				return
			}

			require.NotNil(t, entry.Frequency, "frequency should be populated for a frequency-based trip")
			assert.Equal(t, tt.wantExactTimes, entry.Frequency.ExactTimes)
			assert.Equal(t, tt.wantHeadway, entry.Frequency.Headway.Duration)
			// UTC agency → serviceMidnight is 2025-06-12; the fixture windows
			// span 06:00–09:00 UTC. Compare instants (millis): ModelTime
			// round-trips through JSON in time.Local.
			assert.Equal(t, time.Date(2025, 6, 12, 6, 0, 0, 0, time.UTC).UnixMilli(), entry.Frequency.StartTime.UnixMilli())
			assert.Equal(t, time.Date(2025, 6, 12, 9, 0, 0, 0, time.UTC).UnixMilli(), entry.Frequency.EndTime.UnixMilli())
		})
	}
}

func TestArrivalAndDepartureForStopHandlerWithNonexistentStopID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tripID := utils.FormCombinedID("25", "9e7be5e4-3de9-456d-a174-d79bbbe40f80")
	serviceDate := time.Now().UnixMilli()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/agency_invalid.json?key=TEST&tripId=%s&serviceDate=%d", tripID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Equal(t, EntryData[models.ArrivalAndDeparture]{}, model.Data)
}

func TestArrivalAndDepartureForStopHandlerWithTimeParameter(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := utils.FormCombinedID("25", "5004")
	tripID := utils.FormCombinedID("25", "9e7be5e4-3de9-456d-a174-d79bbbe40f80")
	specificTime := time.Now().Add(time.Hour)
	serviceDate := utils.CalculateServiceDate(specificTime)
	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&time=%d",
		stopID, tripID, serviceDate.UnixMilli(), specificTime.UnixMilli())
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, stopID, model.Data.Entry.StopID)
	assert.Equal(t, tripID, model.Data.Entry.TripID)
}

func TestArrivalAndDepartureForStopHandlerRequiresTripId(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	stop := mustGetStop(t, api)

	stopID := utils.FormCombinedID(agency.ID, stop.ID)
	serviceDate := time.Now().UnixMilli()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&serviceDate=%d", stopID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, model.Data.FieldErrors, "tripId")
	assert.Len(t, model.Data.FieldErrors["tripId"], 1)
	assert.Equal(t, "missingRequiredField", model.Data.FieldErrors["tripId"][0])
}

func TestArrivalAndDepartureForStopHandlerRequiresServiceDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	stop := mustGetStop(t, api)
	trip := mustGetTrip(t, api)

	stopID := utils.FormCombinedID(agency.ID, stop.ID)
	tripID := utils.FormCombinedID(agency.ID, trip.ID)
	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s", stopID, tripID)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, model.Data.FieldErrors, "serviceDate")
	assert.Len(t, model.Data.FieldErrors["serviceDate"], 1)
	assert.Equal(t, "missingRequiredField", model.Data.FieldErrors["serviceDate"][0])
}

func TestArrivalAndDepartureForStopHandlerWithStopSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := utils.FormCombinedID("25", "3028")
	tripID := utils.FormCombinedID("25", "03969589-98dc-4fcd-a1c2-ce084b4ca5d2")
	serviceDate := time.Now()
	stopSequence := 19
	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=%d", stopID, tripID, serviceDate.UnixMilli(), stopSequence)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, stopID, model.Data.Entry.StopID)

	entry := model.Data.Entry
	assert.Equal(t, stopID, entry.StopID)
	assert.Equal(t, tripID, entry.TripID)
	// serviceDate in response is midnight of the service date in the agency's timezone,
	// not the raw input epoch. The RABA agency uses America/Los_Angeles.
	agencyLoc, _ := time.LoadLocation("America/Los_Angeles")
	sdTime := serviceDate.In(agencyLoc)
	expectedMidnight := time.Date(sdTime.Year(), sdTime.Month(), sdTime.Day(), 0, 0, 0, 0, agencyLoc)
	assert.Equal(t, expectedMidnight, entry.ServiceDate.In(agencyLoc))
	assert.False(t, entry.ScheduledArrivalTime.IsZero())
	assert.False(t, entry.ScheduledDepartureTime.IsZero())
	assert.True(t, entry.ArrivalEnabled)
	assert.True(t, entry.DepartureEnabled)
	assert.Equal(t, int(stopSequence-1), entry.StopSequence) // Zero-based
	assert.NotZero(t, entry.TotalStopsInTrip)

	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
	assert.Contains(t, model.Data.References.Routes, testdata.Route3)
	assert.NotEmpty(t, model.Data.References.Trips)
	assert.NotEmpty(t, model.Data.References.Stops)
}

func TestArrivalAndDepartureForStopHandlerWithMinutesParameters(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := utils.FormCombinedID("25", "2014")
	tripID := utils.FormCombinedID("25", "9747e238-4509-4d1e-bbe3-5aa5d1b43641")
	serviceDate := time.Now().UnixMilli()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&minutesBefore=10&minutesAfter=60", stopID, tripID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, stopID, model.Data.Entry.StopID)
}

func TestArrivalAndDepartureForStopHandlerWithNonexistentTripID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	stop := mustGetStop(t, api)

	stopID := utils.FormCombinedID(agency.ID, stop.ID)
	tripID := utils.FormCombinedID(agency.ID, "nonexistent_trip")
	serviceDate := time.Now().UnixMilli()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestArrivalAndDepartureForStopHandlerWithMalformedTripID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	stop := mustGetStop(t, api)

	stopID := utils.FormCombinedID(agency.ID, stop.ID)
	tripID := "malformedid" // No underscore, will fail extraction
	serviceDate := time.Now().UnixMilli()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestArrivalAndDepartureForStopHandlerWithMalformedStopID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trip := mustGetTrip(t, api)

	stopID := "malformedid" // No underscore, will fail extraction
	tripID := utils.FormCombinedID(agency.ID, trip.ID)
	serviceDate := time.Now().UnixMilli()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestArrivalAndDepartureForStopHandlerWithValidTripAndStopSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	ctx := context.Background()

	trips, err := api.GtfsManager.GetTrips(ctx, 100)
	require.NoError(t, err)
	require.NotEmpty(t, trips)

	var validTripID, validStopID string
	var stopSequence int64

	for _, trip := range trips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if err == nil && len(stopTimes) >= 2 {
			validTripID = trip.ID
			validStopID = stopTimes[1].StopID
			stopSequence = stopTimes[1].StopSequence
			break
		}
	}
	require.NotEmpty(t, validTripID, "No valid trip with multiple stops found in test data")

	combinedStopID := utils.FormCombinedID(agency.ID, validStopID)
	combinedTripID := utils.FormCombinedID(agency.ID, validTripID)
	serviceDate := time.Now()

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=%d", combinedStopID, combinedTripID, serviceDate.UnixMilli(), stopSequence)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, int(stopSequence-1), model.Data.Entry.StopSequence) // Zero-based
}

func TestArrivalAndDepartureForStopHandlerWithWrongStopSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	trips, err := api.GtfsManager.GetTrips(t.Context(), 100)
	require.NoError(t, err)
	require.NotEmpty(t, trips)

	var validTripID, validStopID string
	var stopSequence int64
	for _, trip := range trips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(t.Context(), trip.ID)
		if err == nil && len(stopTimes) >= 2 {
			validTripID = trip.ID
			validStopID = stopTimes[1].StopID
			stopSequence = stopTimes[1].StopSequence
			break
		}
	}
	require.NotEmpty(t, validTripID, "No valid trip with multiple stops found in test data")

	combinedStopID := utils.FormCombinedID(agency.ID, validStopID)
	combinedTripID := utils.FormCombinedID(agency.ID, validTripID)
	serviceDate := time.Now()
	wrongSequence := stopSequence + 100

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=%d", combinedStopID, combinedTripID, serviceDate.UnixMilli(), wrongSequence)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

func TestGetPredictedTimes_NoRealTimeData(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	scheduledArrival := time.Now()
	scheduledDeparture := scheduledArrival.Add(2 * time.Minute)

	predArrival, predDeparture, predicted := api.getPredictedTimes("nonexistent_trip", "nonexistent_stop", 1, scheduledArrival, scheduledDeparture)

	assert.True(t, predArrival.IsZero())
	assert.True(t, predDeparture.IsZero())
	assert.False(t, predicted)
}

func TestGetPredictedTimes_EqualArrivalDeparture(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	scheduledTime := time.Now()

	predArrival, predDeparture, predicted := api.getPredictedTimes("test_trip", "test_stop", 1, scheduledTime, scheduledTime)

	assert.True(t, predArrival.IsZero())
	assert.True(t, predDeparture.IsZero())
	assert.False(t, predicted)
}

func TestParseArrivalAndDepartureParams_AllParameters(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test?minutesAfter=60&minutesBefore=15&time=1609459200000&tripId=trip_123&serviceDate=1609459200000&vehicleId=vehicle_456&stopSequence=3", nil)

	params, errs := parseArrivalAndDepartureParams(req)

	assert.Empty(t, errs)

	assert.Equal(t, 60, params.MinutesAfter)
	assert.Equal(t, 15, params.MinutesBefore)
	assert.NotNil(t, params.Time)
	assert.Equal(t, "trip_123", params.TripID)
	assert.NotNil(t, params.ServiceDate)
	assert.Equal(t, "vehicle_456", params.VehicleID)
	require.NotNil(t, params.StopSequence)
	assert.Equal(t, 3, *params.StopSequence)
}

func TestParseArrivalAndDepartureParams_DefaultValues(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test", nil)

	params, errs := parseArrivalAndDepartureParams(req)

	assert.Empty(t, errs)

	assert.Equal(t, 30, params.MinutesAfter)
	assert.Equal(t, 5, params.MinutesBefore)
	assert.Nil(t, params.Time)
	assert.Equal(t, "", params.TripID)
	assert.Nil(t, params.ServiceDate)
	assert.Equal(t, "", params.VehicleID)
	assert.Nil(t, params.StopSequence)
}

func TestParseArrivalAndDepartureParams_InvalidValues(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?minutesAfter=invalid&minutesBefore=invalid&time=invalid&serviceDate=invalid&stopSequence=invalid", nil)

	_, errs := parseArrivalAndDepartureParams(req)

	assert.Contains(t, errs, "minutesAfter")
	assert.Contains(t, errs, "minutesBefore")
	assert.Contains(t, errs, "time")
	assert.Contains(t, errs, "serviceDate")
	assert.Contains(t, errs, "stopSequence")

	assert.Equal(t, "must be a valid integer", errs["minutesAfter"][0])
	assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errs["serviceDate"][0])
}

func TestParseArrivalAndDepartureParams_NegativeValues(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?minutesAfter=-5&minutesBefore=-1", nil)

	_, errs := parseArrivalAndDepartureParams(req)

	assert.Contains(t, errs, "minutesAfter")
	assert.Contains(t, errs, "minutesBefore")
	assert.Equal(t, "must be a non-negative integer", errs["minutesAfter"][0])
	assert.Equal(t, "must be a non-negative integer", errs["minutesBefore"][0])
}

func TestParseArrivalAndDepartureParams_LargeValues(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?minutesAfter=9999&minutesBefore=9999", nil)

	params, errs := parseArrivalAndDepartureParams(req)

	assert.Empty(t, errs)
	assert.Equal(t, 1440, params.MinutesAfter)
	assert.Equal(t, 1440, params.MinutesBefore)
}

func TestArrivalAndDepartureForStopHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/arrival-and-departure-for-stop/" + malformedID + ".json?key=TEST"

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestArrivalsAndDeparturesForStopHandlerInvalidTime(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/arrival-and-departure-for-stop/1_75403.json?key=TEST&time=invalid_time"

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestArrivalAndDepartureForStopHandler_MultiAgency_Regression(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := t.Context()
	queries := api.GtfsManager.GtfsDB.Queries

	// 1. Setup: Agency A owns the stop
	agencyA := "AgencyA"
	stopID := "MultiAgencyStop"
	_, err := queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       agencyA,
		Name:     "Transit Agency A",
		Url:      "http://agency-a.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:   stopID,
		Name: nulls.String("Shared Transit Center"),
		Lat:  47.6062,
		Lon:  -122.3321,
	})
	require.NoError(t, err)

	// 2. Setup: Agency B owns the route
	agencyB := "AgencyB"
	routeB_ID := "RouteB"
	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       agencyB,
		Name:     "Transit Agency B",
		Url:      "http://agency-b.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID:        routeB_ID,
		AgencyID:  agencyB,
		ShortName: nulls.String("B-Line"),
		LongName:  nulls.String("Agency B Express"),
		Type:      3,
	})
	require.NoError(t, err)

	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:        "service1",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20200101",
		EndDate:   "20301231",
	})
	require.NoError(t, err)

	tripB_ID := "TripB"
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:           tripB_ID,
		RouteID:      routeB_ID,
		ServiceID:    "service1",
		TripHeadsign: nulls.String("Downtown"),
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripB_ID,
		StopID:        stopID,
		StopSequence:  1,
		ArrivalTime:   28800 * 1e9,
		DepartureTime: 29100 * 1e9,
	})
	require.NoError(t, err)

	combinedStopID := utils.FormCombinedID(agencyA, stopID)
	combinedTripID := utils.FormCombinedID(agencyB, tripB_ID)
	serviceDate := time.Now().UnixMilli()

	endpoint :=
		fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", combinedStopID, combinedTripID, serviceDate)
	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	expectedRouteID := utils.FormCombinedID(agencyB, routeB_ID)
	assert.Equal(t, expectedRouteID, model.Data.Entry.RouteID,
		"routeId should use the route's agency (AgencyB), not the stop's agency (AgencyA)")

	var agencyIDs []string
	for _, ag := range model.Data.References.Agencies {
		agencyIDs = append(agencyIDs, ag.ID)
	}
	assert.ElementsMatch(t, []string{agencyA, agencyB}, agencyIDs, "references.agencies should contain Agency A and B")

	routeIndex := slices.IndexFunc(model.Data.References.Routes, func(r models.Route) bool {
		return r.ID == expectedRouteID
	})
	assert.NotEqual(t, -1, routeIndex, "references.routes should contain the correctly prefixed route")
	assert.Equal(t, agencyB, model.Data.References.Routes[routeIndex].AgencyID, "route's agencyId should be AgencyB")
}

func TestGetPredictedTimes_DelayPropagationLogic(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tripID := "test_trip"
	targetStopSequence := int64(5)

	currentTime := time.Now()
	futureTime := currentTime.Add(30 * time.Minute)
	delayDuration := 120 * time.Second

	uint32Ptr := func(v uint32) *uint32 { return &v }

	mockTrip := gtfs.Trip{
		ID: gtfs.TripID{ID: tripID},
		StopTimeUpdates: []gtfs.StopTimeUpdate{
			{
				StopSequence: uint32Ptr(1),
				Departure: &gtfs.StopTimeEvent{
					Time:  &futureTime,
					Delay: &delayDuration,
				},
			},
		},
	}

	api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{mockTrip})

	scheduledTime := time.Now()
	predArrival, predDeparture, predicted := api.getPredictedTimes(tripID, "test_stop", targetStopSequence, scheduledTime, scheduledTime)

	expectedTime := scheduledTime.Add(delayDuration)
	assert.Equal(t, expectedTime, predArrival, "Arrival time should include 120s delay")
	assert.Equal(t, expectedTime, predDeparture, "Departure time should include 120s delay")
	assert.True(t, predicted, "Should be predicted when delay propagation is available")
}

func TestGetPredictedTimes_TripLevelDelayFallback(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tripID := "test_trip_level_delay"
	targetStopSequence := int64(5)

	currentTime := time.Now()
	futureTime := currentTime.Add(30 * time.Minute)
	delayDuration := 300 * time.Second
	otherStopID := "other_stop"

	mockTrip := gtfs.Trip{
		ID:    gtfs.TripID{ID: tripID},
		Delay: &delayDuration,
		StopTimeUpdates: []gtfs.StopTimeUpdate{
			{
				StopID:  &otherStopID,
				Arrival: &gtfs.StopTimeEvent{Time: &futureTime},
			},
		},
	}

	api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{mockTrip})

	scheduledTime := time.Now()
	predArrival, predDeparture, predicted := api.getPredictedTimes(tripID, "test_stop", targetStopSequence, scheduledTime, scheduledTime)

	expectedTime := scheduledTime.Add(delayDuration)
	assert.True(t, predicted, "Should be predicted when trip-level delay is available")
	assert.Equal(t, expectedTime, predArrival, "Arrival time should include 300s trip-level delay")
	assert.Equal(t, expectedTime, predDeparture, "Departure time should include 300s trip-level delay")
}

// TestGetPredictedTimes_DelayVariants tables getPredictedTimes' delay-only-STU
// and trip-level-Delay fallback paths, including cases whose delay magnitude
// exceeds the ±1h window that Java's GtfsRealtimeSource.java:811-821 rejects
// at RT ingest (strict > 3600s). getPredictedTimes itself applies no such cap
// -- whatever delay survives ingest is added to the scheduled time -- so every
// case here must produce scheduled + delay for both arrival and departure.
func TestGetPredictedTimes_DelayVariants(t *testing.T) {
	stopIDStr := "target_stop"
	seq := uint32(3)
	stopSeqInt := int64(seq)

	stuDelay := func(d time.Duration) []gtfs.StopTimeUpdate {
		return []gtfs.StopTimeUpdate{{
			StopID:       &stopIDStr,
			StopSequence: &seq,
			Arrival:      &gtfs.StopTimeEvent{Delay: &d},
			Departure:    &gtfs.StopTimeEvent{Delay: &d},
		}}
	}

	cases := []struct {
		name            string
		tripID          string
		lookupStopID    string
		lookupStopSeq   int64
		delay           time.Duration
		stopTimeUpdates []gtfs.StopTimeUpdate
		tripLevelDelay  *time.Duration
	}{
		{
			name:            "delay-only STU (no absolute time) is accepted",
			tripID:          "delay_only_stus_trip",
			lookupStopID:    stopIDStr,
			lookupStopSeq:   stopSeqInt,
			delay:           120 * time.Second,
			stopTimeUpdates: stuDelay(120 * time.Second),
		},
		{
			name:           "trip-level Delay with no STUs is accepted",
			tripID:         "trip_level_delay_no_stus",
			lookupStopID:   "any_stop",
			lookupStopSeq:  5,
			delay:          240 * time.Second,
			tripLevelDelay: ptrDuration(240 * time.Second),
		},
		{
			// Java applies the deviation unconditionally in
			// setPredictedTimesFromScheduleDeviation; the ±1h filter lives
			// at RT ingestion, not at prediction emission. A rider must see
			// a genuinely-late bus as late.
			name:           "trip-level Delay > +1h still emits (no prediction-time cap)",
			tripID:         "very_late_trip_level",
			lookupStopID:   "any_stop",
			lookupStopSeq:  5,
			delay:          70 * time.Minute,
			tripLevelDelay: ptrDuration(70 * time.Minute),
		},
		{
			name:            "per-STU Delay > +1h still emits (no prediction-time cap)",
			tripID:          "very_late_stu_delay",
			lookupStopID:    stopIDStr,
			lookupStopSeq:   stopSeqInt,
			delay:           90 * time.Minute,
			stopTimeUpdates: stuDelay(90 * time.Minute),
		},
		{
			name:           "exactly +1h delay still emits (strict >)",
			tripID:         "boundary_positive_1h",
			lookupStopID:   "any_stop",
			lookupStopSeq:  5,
			delay:          60 * time.Minute,
			tripLevelDelay: ptrDuration(60 * time.Minute),
		},
		{
			name:           "exactly -1h delay still emits (strict >)",
			tripID:         "boundary_negative_1h",
			lookupStopID:   "any_stop",
			lookupStopSeq:  5,
			delay:          -60 * time.Minute,
			tripLevelDelay: ptrDuration(-60 * time.Minute),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := createTestApi(t)
			defer api.Shutdown()

			mockTrip := gtfs.Trip{
				ID:              gtfs.TripID{ID: tc.tripID},
				Delay:           tc.tripLevelDelay,
				StopTimeUpdates: tc.stopTimeUpdates,
			}
			api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{mockTrip})

			scheduledTime := time.Now()
			predArrival, predDeparture, predicted := api.getPredictedTimes(
				tc.tripID, tc.lookupStopID, tc.lookupStopSeq, scheduledTime, scheduledTime,
			)

			expected := scheduledTime.Add(tc.delay)
			assert.True(t, predicted, "predicted flag must be true")
			assert.Equal(t, expected, predArrival, "arrival must be scheduled + delay")
			assert.Equal(t, expected, predDeparture, "departure must be scheduled + delay")
		})
	}
}

func ptrDuration(d time.Duration) *time.Duration { return &d }

func TestArrivalAndDepartureForStop_PositiveUTCOffset_ServiceDateRegression(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := t.Context()
	queries := api.GtfsManager.GtfsDB.Queries

	const (
		agencyID  = "BerlinAgency"
		routeID   = "BerlinRoute"
		tripID    = "BerlinTrip"
		stopID    = "BerlinStop"
		serviceID = "BerlinService"
		timezone  = "Europe/Berlin"
	)

	loc, err := time.LoadLocation(timezone)
	require.NoError(t, err)

	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Berlin Transit", Url: "https://example.com", Timezone: timezone,
	})
	require.NoError(t, err)

	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, Type: 3,
	})
	require.NoError(t, err)

	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: stopID, Lat: 52.52, Lon: 13.405,
	})
	require.NoError(t, err)

	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:     serviceID,
		Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		Saturday: 0, Sunday: 0,
		StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)

	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: serviceID,
	})
	require.NoError(t, err)

	arrivalNs := int64(8 * time.Hour)
	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: tripID, StopID: stopID, StopSequence: 1,
		ArrivalTime: arrivalNs, DepartureTime: arrivalNs,
	})
	require.NoError(t, err)

	midnightJan15CET := time.Date(2025, 1, 15, 0, 0, 0, 0, loc)
	require.Equal(t, 14, midnightJan15CET.UTC().Day(), "precondition: UTC day should be 14")
	require.Equal(t, 15, midnightJan15CET.Day(), "precondition: CET day should be 15")

	combinedStopID := utils.FormCombinedID(agencyID, stopID)
	endpoint := fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=test&tripId=%s&serviceDate=%d&stopSequence=1",
		combinedStopID,
		utils.FormCombinedID(agencyID, tripID),
		midnightJan15CET.UnixMilli(),
	)

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)

	expectedArrival := midnightJan15CET.Add(time.Duration(arrivalNs))
	assert.Equal(t, expectedArrival, model.Data.Entry.ScheduledArrivalTime.In(loc),
		"scheduledArrivalTime should use local date (Jan 15), not UTC date (Jan 14); "+
			"difference of 86400000ms indicates the timezone bug")
}

// Regression test for loop routes where the same stop appears multiple times in a trip.
// Ensures that stopSequence correctly selects among multiple occurrences of the same stop.
func TestArrivalAndDepartureForStopHandler_LoopRouteStopSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := t.Context()
	queries := api.GtfsManager.GtfsDB.Queries

	const (
		agencyID  = "LoopAgency"
		routeID   = "LoopRoute"
		tripID    = "LoopTrip"
		stopID    = "LoopStop"
		serviceID = "LoopService"
	)

	_, err := queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Loop Transit", Url: "https://loop.example.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, Type: 3,
	})
	require.NoError(t, err)

	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:   stopID,
		Name: nulls.String("Loop Stop"),
		Lat:  47.0,
		Lon:  -122.0,
	})
	require.NoError(t, err)

	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:        serviceID,
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20200101",
		EndDate:   "20301231",
	})
	require.NoError(t, err)

	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:        tripID,
		RouteID:   routeID,
		ServiceID: serviceID,
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripID,
		StopID:        stopID,
		StopSequence:  2,
		ArrivalTime:   int64(8 * time.Hour),
		DepartureTime: int64(8 * time.Hour),
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripID,
		StopID:        stopID,
		StopSequence:  15,
		ArrivalTime:   int64(9 * time.Hour),
		DepartureTime: int64(9 * time.Hour),
	})
	require.NoError(t, err)

	combinedStopID := utils.FormCombinedID(agencyID, stopID)
	combinedTripID := utils.FormCombinedID(agencyID, tripID)
	serviceDateMs := time.Now().UnixMilli()

	baseEndpoint := fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d",
		combinedStopID,
		combinedTripID,
		serviceDateMs,
	)

	resp1, model1 := callAPIHandler[ArrivalAndDepartureResponse](t, api, baseEndpoint+"&stopSequence=2")
	require.Equal(t, http.StatusOK, resp1.StatusCode)
	require.Equal(t, http.StatusOK, model1.Code)
	assert.Equal(t, 1, model1.Data.Entry.StopSequence, "expected zero-based index for stop_sequence=2")

	resp2, model2 := callAPIHandler[ArrivalAndDepartureResponse](t, api, baseEndpoint+"&stopSequence=15")
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	require.Equal(t, http.StatusOK, model2.Code)
	assert.Equal(t, 14, model2.Data.Entry.StopSequence, "expected zero-based index for stop_sequence=15")
}

func TestArrivalAndDepartureForStop_VehicleWithNilID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	tripID := "36957461-b451-4390-af3a-bc42c51fd473"
	stopID := "5007"
	stopSequence := 6
	combinedStopID := utils.FormCombinedID("25", stopID)
	combinedTripID := utils.FormCombinedID("25", tripID)
	serviceDateMs := time.Now().UnixMilli()

	api.GtfsManager.MockAddVehicleWithOptions("", tripID, "", internalgtfs.MockVehicleOptions{
		NoID: true,
	})

	endpoint := fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=%d",
		combinedStopID,
		combinedTripID,
		serviceDateMs,
		stopSequence,
	)

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "", model.Data.Entry.VehicleID, "vehicleId should be empty for vehicle with nil ID")
}

// TestArrivalAndDepartureForStop_CanceledTrip_NumberOfStopsAway pins the
// OBA-spec behavior for numberOfStopsAway when BuildTripStatus cannot produce
// a schedule snapshot for the trip -- which is always the case for CANCELED
// trips. Per arrivals-and-departures-for-stop §11a, 0 is the correct
// unknown-data value in that situation; -1 is a legitimate real reading
// meaning "one stop behind" and must not double as a sentinel. This test
// guards the response emitting 0 for a CANCELED trip.
func TestArrivalAndDepartureForStop_CanceledTrip_NumberOfStopsAway(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	tripID := "36957461-b451-4390-af3a-bc42c51fd473"
	stopID := "5007"
	stopSequence := 6
	combinedStopID := utils.FormCombinedID("25", stopID)
	combinedTripID := utils.FormCombinedID("25", tripID)
	serviceDateMs := time.Now().UnixMilli()

	api.GtfsManager.MockAddVehicleWithOptions("canceled-vehicle", tripID, "", internalgtfs.MockVehicleOptions{
		ScheduleRelationship: gtfsrt.TripDescriptor_CANCELED,
	})

	endpoint := fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=%d",
		combinedStopID,
		combinedTripID,
		serviceDateMs,
		stopSequence,
	)

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	// Per the OBA spec (arrivals-and-departures-for-stop §11a), when no
	// block location is available -- as with a CANCELED trip -- the
	// unknown-data value for numberOfStopsAway is 0 (absent/zero), not -1.
	// -1 is a legitimate real value meaning "one stop behind" and must not
	// double as a sentinel. Both handlers agree on 0 for the no-data case.
	assert.Equal(t, 0, model.Data.Entry.NumberOfStopsAway,
		"CANCELED trips report numberOfStopsAway=0 (no block location); "+
			"-1 is reserved for the real 'one stop behind' signal")
}

// TestArrivalAndDepartureForStop_ScheduleOnlyBlock_SnapshotMetrics guards the
// handler's schedule-only fallback for numberOfStopsAway and distanceFromStop
// (arrival_and_departure_for_stop_handler.go around the snapshot.metricsForStop
// call). When the RT vehicle carries no Position and no CurrentStopSequence,
// the handler must still derive both values through BuildTripStatus's block
// snapshot -- not silently zero them out. A regression that dropped that
// codepath would show up as numberOfStopsAway=0 for the queried stop, which
// this test would catch: the queried stop sits one hop past the snapshot's
// NextStopIndex, so metricsForStop must return numberOfStopsAway=1.
// distanceFromStop stays 0 because the minimal fixture has no shape data,
// but that 0 is snapshot-derived (target.DistanceAlongBlock - snap.DAB) --
// asserting it pins the arithmetic path.
func TestArrivalAndDepartureForStop_ScheduleOnlyBlock_SnapshotMetrics(t *testing.T) {
	// 2010-01-01 is a Friday; the fixture below enables all weekdays, so the
	// service is active. Clock at 08:02:00 lands between stop A (08:00) and
	// stop B (08:05), pinning the snapshot's NextStopIndex on B.
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	const (
		agencyID  = "sob-agency"
		routeID   = "sob-route"
		serviceID = "sob-svc"
		blockID   = "sob-block"
		tripID    = "sob-trip"
		stopAID   = "sob-stop-a"
		stopBID   = "sob-stop-b"
		stopCID   = "sob-stop-c"
	)

	// The Create* queries above use INSERT OR REPLACE, so re-runs of this
	// test don't collide on their own -- but the fixtures still leak into
	// the shared test DB and can be observed by other tests. Delete them
	// in foreign-key-safe reverse order: stop_times -> trips -> calendar
	// -> routes -> agencies -> stops. block_id lives as a column on trips,
	// so removing the trip clears it automatically. sqlc only exposes
	// table-wide Clear* helpers, so per-ID cleanup goes through the raw DB
	// handle on the same client. Errors are surfaced via require.NoError
	// consistent with the setup path above.
	db := api.GtfsManager.GtfsDB.DB
	t.Cleanup(func() {
		_, err := db.ExecContext(ctx, `DELETE FROM stop_times WHERE trip_id = ?`, tripID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `DELETE FROM trips WHERE id = ?`, tripID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `DELETE FROM calendar WHERE id = ?`, serviceID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, routeID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `DELETE FROM agencies WHERE id = ?`, agencyID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`DELETE FROM stops WHERE id IN (?, ?, ?)`, stopAID, stopBID, stopCID)
		require.NoError(t, err)
	})

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Schedule-Only Block Agency", Url: "http://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	for _, sid := range []string{stopAID, stopBID, stopCID} {
		_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
			ID: sid, Name: nulls.String(sid), Lat: 47.0, Lon: -122.0,
		})
		require.NoError(t, err)
	}
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID,
		ShortName: nulls.String("SOB"),
		LongName:  nulls.String("Schedule-Only Block"),
		Type:      3,
	})
	require.NoError(t, err)
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: serviceID, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20100101", EndDate: "20301231",
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: serviceID,
		BlockID: nulls.String(blockID),
	})
	require.NoError(t, err)

	// Three stops, 5 minutes apart, spanning 08:00-08:10.
	type stopTime struct {
		stopID string
		seq    int64
		offset time.Duration
	}
	for _, st := range []stopTime{
		{stopAID, 1, 8 * time.Hour},
		{stopBID, 2, 8*time.Hour + 5*time.Minute},
		{stopCID, 3, 8*time.Hour + 10*time.Minute},
	} {
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: st.stopID, StopSequence: st.seq,
			ArrivalTime:   int64(st.offset),
			DepartureTime: int64(st.offset),
		})
		require.NoError(t, err)
	}

	// Vehicle carries neither Position nor CurrentStopSequence -- the handler
	// must fall back to the schedule-derived snapshot for the metrics.
	api.GtfsManager.MockAddVehicleWithOptions("sob-vehicle", tripID, routeID, internalgtfs.MockVehicleOptions{})

	serviceMidnight := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	endpoint := fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=3",
		utils.FormCombinedID(agencyID, stopCID),
		utils.FormCombinedID(agencyID, tripID),
		serviceMidnight.UnixMilli(),
	)

	resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)

	// The snapshot's NextStopIndex is B (block sequence 1); the queried
	// stop C is at block sequence 2. metricsForStop returns
	// target.BlockSequence - stops[NextStopIndex].BlockSequence = 2 - 1 = 1.
	// A 0 here would mean the metricsForStop / snapshot path never ran.
	assert.Equal(t, 1, model.Data.Entry.NumberOfStopsAway,
		"schedule-only snapshot must place the queried stop one hop past "+
			"the next stop; a 0 would mean the snapshot path did not run")
	// distanceFromStop is snapshot-derived (target.DAB - snap.DAB). With no
	// shape data both DABs are 0, so the difference is 0 -- but that 0 comes
	// from the arithmetic, not from the "no snapshot" default.
	assert.InDelta(t, 0.0, model.Data.Entry.DistanceFromStop, 0.001,
		"schedule-only snapshot with no shape data yields distanceFromStop=0 "+
			"via the arithmetic path, not via the metricsForStop-skipped default")
}
