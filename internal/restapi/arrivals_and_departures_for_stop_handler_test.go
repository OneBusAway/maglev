package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

func arrivalsAndDeparturesURL(stopID string, extras ...string) string {
	var b strings.Builder
	b.WriteString("/api/where/arrivals-and-departures-for-stop/")
	b.WriteString(stopID)
	b.WriteString(".json?key=TEST")
	for _, e := range extras {
		b.WriteByte('&')
		b.WriteString(e)
	}
	return b.String()
}

// firstStopID returns a combined stop ID built from the first agency and the
// first active stop in the test fixture. Fails the test if the fixture has no
// stops (RABA always does).
func firstStopID(t *testing.T, api *RestAPI) string {
	t.Helper()
	agency := mustGetAgencies(t, api)[0]
	stops := mustGetStops(t, api)
	require.NotEmpty(t, stops, "fixture should contain at least one stop")
	return utils.FormCombinedID(agency.ID, stops[0].ID)
}

func TestArrivalsAndDeparturesForStopHandlerRequiresValidApiKey(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()
	time.Sleep(500 * time.Millisecond)

	stopID := firstStopID(t, api)

	resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestArrivalsAndDeparturesForStopHandlerEndToEnd(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()
	time.Sleep(500 * time.Millisecond)

	stopID := firstStopID(t, api)

	resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(stopID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.NotZero(t, model.CurrentTime)

	entry := model.Data.Entry
	assert.Equal(t, stopID, entry.StopID)
	assert.NotNil(t, entry.NearbyStopIDs)
	assert.NotNil(t, entry.SituationIDs)

	require.NotEmpty(t, model.Data.References.Agencies)

	if len(entry.ArrivalsAndDepartures) > 0 {
		first := entry.ArrivalsAndDepartures[0]
		assert.Equal(t, stopID, first.StopID)
		assert.NotEmpty(t, first.RouteID)
		assert.NotEmpty(t, first.TripID)
		assert.NotEmpty(t, first.TripHeadsign)

		require.NotEmpty(t, model.Data.References.Routes)
		require.NotEmpty(t, model.Data.References.Trips)
		require.NotEmpty(t, model.Data.References.Stops)
	}
}

func TestArrivalsAndDeparturesForStopHandlerTimeParams(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()
	time.Sleep(500 * time.Millisecond)

	stopID := firstStopID(t, api)
	tomorrow := time.Now().AddDate(0, 0, 1)
	specificTime := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, time.Local)
	specificTimeMs := specificTime.Unix() * 1000

	tests := []struct {
		name   string
		extras []string
	}{
		{"minutesAfter and minutesBefore", []string{"minutesAfter=60", "minutesBefore=10"}},
		{"absolute time param", []string{"time=" + strconv.FormatInt(specificTimeMs, 10)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(stopID, tt.extras...))

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
			assert.Equal(t, stopID, model.Data.Entry.StopID)
			require.NotEmpty(t, model.Data.References.Agencies)
		})
	}
}

func TestArrivalsAndDeparturesForStopHandlerWithInvalidStopID(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()
	time.Sleep(500 * time.Millisecond)

	agency := mustGetAgencies(t, api)[0]
	invalidStopID := utils.FormCombinedID(agency.ID, "invalid_stop")

	resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(invalidStopID))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestArrivalsAndDeparturesForStopHandlerInvalidIDs(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	tests := []struct {
		name           string
		stopID         string
		expectedStatus int
	}{
		{"Malformed format (looks valid but unknown)", "invalid_format", http.StatusNotFound},
		{"No agency separator", "1110", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(tt.stopID))

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Equal(t, tt.expectedStatus, model.Code)
		})
	}
}

func TestArrivalsAndDeparturesForStopHandlerNoActiveServices(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()
	time.Sleep(500 * time.Millisecond)

	stopID := firstStopID(t, api)
	futureTime := time.Now().AddDate(10, 0, 0)
	timeMs := futureTime.Unix() * 1000

	resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
		arrivalsAndDeparturesURL(stopID, "time="+strconv.FormatInt(timeMs, 10)))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, stopID, model.Data.Entry.StopID)
	assert.Empty(t, model.Data.Entry.ArrivalsAndDepartures)
	assert.NotNil(t, model.Data.Entry.NearbyStopIDs)
	assert.NotNil(t, model.Data.Entry.SituationIDs)
	require.NotEmpty(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Trips)
}

func TestParseArrivalsAndDeparturesParams_AllParameters(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	req := httptest.NewRequest("GET", "/test?minutesAfter=60&minutesBefore=15&time=1609459200000", nil)

	params, errs := api.parseArrivalsAndDeparturesParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 60*time.Minute, params.After)
	assert.Equal(t, 15*time.Minute, params.Before)
	assert.False(t, params.Time.IsZero())
}

func TestParseArrivalsAndDeparturesParams_DefaultValues(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	req := httptest.NewRequest("GET", "/test", nil)

	params, errs := api.parseArrivalsAndDeparturesParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 35*time.Minute, params.After) // Default for plural handler
	assert.Equal(t, 5*time.Minute, params.Before)
	assert.WithinDuration(t, api.Clock.Now(), params.Time, 1*time.Second)
}

func TestParseArrivalsAndDeparturesParams_InvalidValues(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	req := httptest.NewRequest("GET", "/test?minutesAfter=invalid&minutesBefore=invalid&time=invalid", nil)

	_, errs := api.parseArrivalsAndDeparturesParams(req)

	require.NotNil(t, errs)
	assert.Equal(t, "must be a valid integer", errs["minutesAfter"][0])
	assert.Equal(t, "must be a valid integer", errs["minutesBefore"][0])
	assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errs["time"][0])
}

func TestArrivalsAndDeparturesForStopHandlerWithInvalidParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := firstStopID(t, api)

	tests := []struct {
		name  string
		extra string
	}{
		{"invalid time", "time=invalid"},
		{"invalid minutesAfter", "minutesAfter=invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(stopID, tt.extra))
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestArrivalsAndDeparturesForStopHandler_MultiAgency_Regression(t *testing.T) {
	// Use a MockClock within the service window so the plural handler finds the trip.
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, loc))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	ctx := context.Background()
	queries := api.GtfsManager.GtfsDB.Queries

	const (
		agencyA  = "AgencyA"
		stopID   = "MultiAgencyStop"
		agencyB  = "AgencyB"
		routeBID = "RouteB"
		tripBID  = "TripB"
	)
	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyA, Name: "Transit Agency A", Url: "http://agency-a.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)
	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: stopID, Name: nulls.String("Shared Transit Center"),
		Lat: 47.6062, Lon: -122.3321,
	})
	require.NoError(t, err)
	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyB, Name: "Transit Agency B", Url: "http://agency-b.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)
	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeBID, AgencyID: agencyB,
		ShortName: nulls.String("B-Line"),
		LongName:  nulls.String("Agency B Express"),
		Type:      3,
	})
	require.NoError(t, err)
	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: "service1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20000101", EndDate: "20301231",
	})
	require.NoError(t, err)
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripBID, RouteID: routeBID, ServiceID: "service1",
		TripHeadsign: nulls.String("Downtown"),
	})
	require.NoError(t, err)
	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: tripBID, StopID: stopID, StopSequence: 1,
		ArrivalTime:   28800 * 1e9, // 08:00:00
		DepartureTime: 29100 * 1e9, // 08:05:00
	})
	require.NoError(t, err)

	combinedStopID := utils.FormCombinedID(agencyA, stopID)
	expectedRouteID := utils.FormCombinedID(agencyB, routeBID)

	resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected arrivals for multi-agency stop")
	first := model.Data.Entry.ArrivalsAndDepartures[0]
	assert.Equal(t, expectedRouteID, first.RouteID,
		"routeId should use the route's agency (AgencyB), not the stop's agency (AgencyA)")

	agencyIDs := make(map[string]bool)
	for _, ag := range model.Data.References.Agencies {
		agencyIDs[ag.ID] = true
	}
	assert.True(t, agencyIDs[agencyA], "references.agencies should contain Agency A")
	assert.True(t, agencyIDs[agencyB], "references.agencies should contain Agency B")

	require.NotEmpty(t, model.Data.References.Routes)
	foundRoute := false
	for _, r := range model.Data.References.Routes {
		if r.ID == expectedRouteID {
			foundRoute = true
			assert.Equal(t, agencyB, r.AgencyID, "route's agencyId should be AgencyB")
			break
		}
	}
	assert.True(t, foundRoute, "references.routes should contain the correctly prefixed route")
}

func TestArrivalsAndDeparturesReturnsResultsNearMidnight(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := mustGetAgencies(t, api)[0]
	stops := mustGetStops(t, api)
	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	foundResults := false
	for _, stop := range stops {
		stopID := utils.FormCombinedID(agency.ID, stop.ID)
		url := arrivalsAndDeparturesURL(stopID, "minutesBefore=15", "minutesAfter=240")
		resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, url)
		if resp.StatusCode == http.StatusOK && len(model.Data.Entry.ArrivalsAndDepartures) > 0 {
			foundResults = true
			break
		}
	}

	assert.True(t, foundResults, "Should find at least one stop with early morning arrivals near midnight boundary")
}

// setupDelayPropTestData inserts a minimal set of DB records for testing the delay
// propagation logic. The MockClock must be at 2010-01-01 08:02:00 UTC so that
// the default 5-min-before / 35-min-after window covers the 08:00:00 arrival.
// stopSeq is the stop_sequence value written to the DB for the stop being queried.
func setupDelayPropTestData(t *testing.T, api *RestAPI, stopSeq int64) (stopCode, combinedStopID, tripID string, scheduledArrivalMs int64) {
	t.Helper()
	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	agencyID := "dp-agency"
	stopCode = "dp-stop"
	routeID := "dp-route"
	tripID = "dp-trip"
	serviceID := "dp-svc"

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Delay Prop Agency", Url: "http://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: stopCode, Name: nulls.String("Delay Test Stop"), Lat: 47.0, Lon: -122.0,
	})
	require.NoError(t, err)
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID,
		ShortName: nulls.String("DT"),
		LongName:  nulls.String("Delay Test Route"),
		Type:      3,
	})
	require.NoError(t, err)
	// 2010-01-01 is a Friday; cover all days to keep setup simple.
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: serviceID, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20100101", EndDate: "20301231",
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: serviceID,
		BlockID: nulls.String("dp-block"),
	})
	require.NoError(t, err)

	arrivalNanos := int64(28800) * 1e9 // 08:00:00 as nanoseconds since midnight
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: tripID, StopID: stopCode, StopSequence: stopSeq,
		ArrivalTime:   arrivalNanos,
		DepartureTime: arrivalNanos + int64(300)*1e9, // 08:05:00
	})
	require.NoError(t, err)

	combinedStopID = utils.FormCombinedID(agencyID, stopCode)
	serviceMidnight := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	scheduledArrivalMs = serviceMidnight.Add(time.Duration(arrivalNanos)).UnixMilli()
	return
}

// TestPluralArrivals_ExactStopMatch verifies that a StopTimeUpdate matching the
// queried stop (by stop ID) is applied directly and marks the arrival as predicted.
func TestPluralArrivals_ExactStopMatch(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopCode, combinedStopID, tripID, scheduledArrivalMs := setupDelayPropTestData(t, api, 2)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")
	delay := 60 * time.Second
	seq := uint32(2)
	api.GtfsManager.MockAddTripUpdate(tripID, nil, []gtfs.StopTimeUpdate{
		{
			StopID: &stopCode, StopSequence: &seq,
			Arrival:   &gtfs.StopTimeEvent{Delay: &delay},
			Departure: &gtfs.StopTimeEvent{Delay: &delay},
		},
	})

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	scheduledDepartureMs := scheduledArrivalMs + 300000 // departure is 5 min after arrival
	assert.True(t, a.Predicted, "exact stop match should be predicted")
	assert.Equal(t, scheduledArrivalMs+60000, a.PredictedArrivalTime.UnixMilli(),
		"predicted arrival should be scheduled + 60s")
	assert.Equal(t, scheduledDepartureMs+60000, a.PredictedDepartureTime.UnixMilli(),
		"predicted departure should be scheduled + 60s")
}

// TestPluralArrivals_PriorStopPropagation verifies that when no StopTimeUpdate
// matches the queried stop, the delay is propagated from the closest prior stop.
func TestPluralArrivals_PriorStopPropagation(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// Stop being queried is sequence 3; prior update is at sequence 2.
	_, combinedStopID, tripID, scheduledArrivalMs := setupDelayPropTestData(t, api, 3)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")
	priorSeq := uint32(2)
	delay := 90 * time.Second
	api.GtfsManager.MockAddTripUpdate(tripID, nil, []gtfs.StopTimeUpdate{
		{StopSequence: &priorSeq, Arrival: &gtfs.StopTimeEvent{Delay: &delay}},
	})

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	scheduledDepartureMs := scheduledArrivalMs + 300000

	// The queried stop is at seq=3 (0-based stopSequence=2). Prior tests may have inserted
	// a stop at seq=2 into the shared DB, so we locate the right arrival explicitly.
	var found bool
	for _, a := range model.Data.Entry.ArrivalsAndDepartures {
		if a.StopSequence != 2 {
			continue
		}
		found = true
		assert.True(t, a.Predicted, "should be predicted via prior stop propagation")
		assert.Equal(t, scheduledArrivalMs+90000, a.PredictedArrivalTime.UnixMilli(),
			"predicted arrival should be scheduled + propagated 90s delay")
		assert.Equal(t, scheduledDepartureMs+90000, a.PredictedDepartureTime.UnixMilli(),
			"predicted departure should be scheduled + propagated 90s delay")
		break
	}
	assert.True(t, found, "expected to find the propagated arrival for seq=3")
}

// TestPluralArrivals_TripLevelDelayFallback verifies that when a TripUpdate has a
// trip-level Delay but no StopTimeUpdates, that delay is applied to the arrival.
func TestPluralArrivals_TripLevelDelayFallback(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, scheduledArrivalMs := setupDelayPropTestData(t, api, 1)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")
	tripDelay := 120 * time.Second
	api.GtfsManager.MockAddTripUpdate(tripID, &tripDelay, nil)

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	scheduledDepartureMs := scheduledArrivalMs + 300000
	assert.True(t, a.Predicted, "trip-level delay should mark arrival as predicted")
	assert.Equal(t, scheduledArrivalMs+120000, a.PredictedArrivalTime.UnixMilli(),
		"predicted arrival should be scheduled + trip-level 120s delay")
	assert.Equal(t, scheduledDepartureMs+120000, a.PredictedDepartureTime.UnixMilli(),
		"predicted departure should be scheduled + trip-level 120s delay")
}

// TestPluralArrivals_TripLevelDelayWithoutVehicle verifies that when a TripUpdate has a
// trip-level Delay but no vehicle position exists, the prediction still applies.
// Prediction is no longer gated on vehicle != nil.
func TestPluralArrivals_TripLevelDelayWithoutVehicle(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, scheduledArrivalMs := setupDelayPropTestData(t, api, 1)
	tripDelay := 120 * time.Second
	api.GtfsManager.MockAddTripUpdate(tripID, &tripDelay, nil) // NO vehicle added

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	scheduledDepartureMs := scheduledArrivalMs + 300000
	assert.True(t, a.Predicted, "trip-level delay without vehicle should still be predicted")
	assert.Equal(t, scheduledArrivalMs+120000, a.PredictedArrivalTime.UnixMilli())
	assert.Equal(t, scheduledDepartureMs+120000, a.PredictedDepartureTime.UnixMilli())
}

// TestPluralArrivals_TripUpdateWithoutVehicle verifies that a stop-level
// StopTimeUpdate produces predictions even when no vehicle position exists.
// (Sibling: TripLevelDelayWithoutVehicle covers the trip-level case.)
func TestPluralArrivals_TripUpdateWithoutVehicle(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, scheduledArrivalMs := setupDelayPropTestData(t, api, 1)

	// Stop-level delay update WITHOUT a vehicle — the absence of MockAddVehicle is the test.
	delay := 120 * time.Second
	seq := uint32(1)
	api.GtfsManager.MockAddTripUpdate(tripID, nil, []gtfs.StopTimeUpdate{
		{
			StopSequence: &seq,
			Arrival:      &gtfs.StopTimeEvent{Delay: &delay},
			Departure:    &gtfs.StopTimeEvent{Delay: &delay},
		},
	})

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	scheduledDepartureMs := scheduledArrivalMs + 300000
	assert.True(t, a.Predicted, "stop-level delay without vehicle should still be predicted")
	assert.Equal(t, scheduledArrivalMs+120000, a.PredictedArrivalTime.UnixMilli(),
		"predicted arrival should be scheduled + 120s delay")
	assert.Equal(t, scheduledDepartureMs+120000, a.PredictedDepartureTime.UnixMilli(),
		"predicted departure should be scheduled + 120s delay")
}

// TestPluralArrivals_NoMatchingOrPriorStop verifies that a TripUpdate with a
// StopTimeUpdate for a later stop does not mark the arrival as predicted.
func TestPluralArrivals_NoMatchingOrPriorStop(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// Stop being queried is sequence 1; update is for sequence 5 (later stop).
	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")
	laterSeq := uint32(5)
	delay := 60 * time.Second
	api.GtfsManager.MockAddTripUpdate(tripID, nil, []gtfs.StopTimeUpdate{
		{StopSequence: &laterSeq, Arrival: &gtfs.StopTimeEvent{Delay: &delay}},
	})

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	assert.False(t, a.Predicted, "update for a later stop should not predict current stop")
	assert.True(t, a.PredictedArrivalTime.IsZero(), "predictedArrivalTime should be zero when not predicted")
	assert.True(t, a.PredictedDepartureTime.IsZero(), "predictedDepartureTime should be zero when not predicted")
}

// TestPluralArrivals_VehiclePositionAloneDoesNotPredict verifies that a vehicle
// position without any TripUpdate does NOT mark the arrival as predicted.
func TestPluralArrivals_VehiclePositionAloneDoesNotPredict(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route") // no trip update

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	assert.False(t, a.Predicted, "vehicle position alone should not mark arrival as predicted")
	assert.True(t, a.PredictedArrivalTime.IsZero())
	assert.True(t, a.PredictedDepartureTime.IsZero())
}

// TestPluralArrivals_AbsoluteTimeStopEvent verifies that when a StopTimeUpdate provides
// absolute Time values for an exact stop match, the predicted times are set directly.
func TestPluralArrivals_AbsoluteTimeStopEvent(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopCode, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 2)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")
	seq := uint32(2)
	absoluteArrival := time.Date(2010, 1, 1, 8, 1, 30, 0, time.UTC)  // 30s early
	absoluteDeparture := time.Date(2010, 1, 1, 8, 6, 0, 0, time.UTC) // 1 min after scheduled departure
	api.GtfsManager.MockAddTripUpdate(tripID, nil, []gtfs.StopTimeUpdate{
		{
			StopID: &stopCode, StopSequence: &seq,
			Arrival:   &gtfs.StopTimeEvent{Time: &absoluteArrival},
			Departure: &gtfs.StopTimeEvent{Time: &absoluteDeparture},
		},
	})

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")
	a := model.Data.Entry.ArrivalsAndDepartures[0]
	assert.True(t, a.Predicted, "absolute-time stop match should be predicted")
	assert.Equal(t, absoluteArrival.Unix()*1000, a.PredictedArrivalTime.UnixMilli(),
		"predictedArrivalTime should equal the absolute arrival timestamp")
	assert.Equal(t, absoluteDeparture.Unix()*1000, a.PredictedDepartureTime.UnixMilli(),
		"predictedDepartureTime should equal the absolute departure timestamp")
}

// TestPluralArrivals_StalePropagatedDelayReset verifies that when the closest prior
// stop has only absolute Time data (no Delay), propagatedDelayMs is reset to 0.
func TestPluralArrivals_StalePropagatedDelayReset(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// Stop being queried is sequence 3.
	_, combinedStopID, tripID, scheduledArrivalMs := setupDelayPropTestData(t, api, 3)
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")

	// Sequence 1: has a 90s delay.
	// Sequence 2 (closer): has only an absolute Time, no Delay.
	// Expected: propagatedDelayMs is reset to 0 when seq 2 becomes the closest prior.
	seq1 := uint32(1)
	delay90s := 90 * time.Second
	seq2 := uint32(2)
	absoluteTime := time.Date(2010, 1, 1, 7, 59, 0, 0, time.UTC)
	api.GtfsManager.MockAddTripUpdate(tripID, nil, []gtfs.StopTimeUpdate{
		{StopSequence: &seq1, Arrival: &gtfs.StopTimeEvent{Delay: &delay90s}},
		{StopSequence: &seq2, Arrival: &gtfs.StopTimeEvent{Time: &absoluteTime}},
	})

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, arrivalsAndDeparturesURL(combinedStopID))

	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "expected at least one arrival")

	// The queried stop is at seq=3 (0-based stopSequence=2). Prior tests may have inserted
	// stops at seq=1 and seq=2 into the shared DB, so we locate the right arrival explicitly.
	var found bool
	for _, a := range model.Data.Entry.ArrivalsAndDepartures {
		if a.StopSequence != 2 {
			continue
		}
		found = true
		assert.True(t, a.Predicted, "should be predicted via prior stop propagation")
		assert.Equal(t, scheduledArrivalMs, a.PredictedArrivalTime.UnixMilli(),
			"propagatedDelayMs should be 0 when closest prior stop only has absolute Time data")
		break
	}
	assert.True(t, found, "expected to find arrival for seq=3 (stopSequence=2)")
}

func TestGetNearbyStopIDs_UsesResolvedAgency(t *testing.T) {
	// Use MockClock within RABA service window (calendar ends 2025-12-31).
	mockClock := clock.NewMockClock(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	ctx := context.Background()

	stops := api.GtfsManager.GetStopsInBounds(ctx, &internalgtfs.LocationParams{Lat: 40.589123, Lon: -122.390830, Radius: 2000}, 10)
	require.NotEmpty(t, stops, "precondition: RABA should have stops near Redding, CA")
	currentStop := stops[0]

	result := getNearbyStopIDs(api, ctx, currentStop.Lat, currentStop.Lon, currentStop.ID, "WrongFallbackAgency")

	require.NotEmpty(t, result, "should find nearby stops")
	for _, combinedID := range result {
		agencyID, _, err := utils.ExtractAgencyIDAndCodeID(combinedID)
		require.NoError(t, err, "combined ID should be parseable: %s", combinedID)
		assert.NotEqual(t, "WrongFallbackAgency", agencyID)
	}
}

func TestGetNearbyStopIDs_ExcludesCurrentStop(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	ctx := context.Background()

	stops := api.GtfsManager.GetStopsInBounds(ctx, &internalgtfs.LocationParams{Lat: 40.589123, Lon: -122.390830, Radius: 2000}, 10)
	require.NotEmpty(t, stops)
	currentStop := stops[0]

	result := getNearbyStopIDs(api, ctx, currentStop.Lat, currentStop.Lon, currentStop.ID, "25")

	for _, combinedID := range result {
		_, codeID, _ := utils.ExtractAgencyIDAndCodeID(combinedID)
		assert.NotEqual(t, currentStop.ID, codeID, "current stop should be excluded from nearby results")
	}
}

func TestArrivalsAndDeparturesForStop_VehicleWithNilID(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	// Use a date distinct from other tests that share the active-service-IDs cache
	// (e.g. TestPluralArrivals_TripUpdateWithoutVehicle uses 2010-01-01).
	// A unique date guarantees a cache miss so "nilid_service" is visible on first query.
	mockClock := clock.NewMockClock(time.Date(2009, 6, 15, 8, 2, 0, 0, loc))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	ctx := context.Background()
	queries := api.GtfsManager.GtfsDB.Queries

	const (
		agencyID = "NilIDAgency"
		stopID   = "NilIDStop"
		routeID  = "NilIDRoute"
		tripID   = "NilIDTrip"
	)
	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Nil ID Test Agency", Url: "http://nilid-agency.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)
	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: stopID, Name: nulls.String("Nil ID Test Stop"),
		Lat: 40.5865, Lon: -122.3917,
	})
	require.NoError(t, err)
	_, err = queries.CreateRoute(ctx, gtfsdb.CreateRouteParams{ID: routeID, AgencyID: agencyID, Type: 3})
	require.NoError(t, err)
	_, err = queries.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: "nilid_service", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20000101", EndDate: "20301231",
	})
	require.NoError(t, err)
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{ID: tripID, RouteID: routeID, ServiceID: "nilid_service"})
	require.NoError(t, err)
	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: tripID, StopID: stopID, StopSequence: 1,
		ArrivalTime: 29100 * 1e9, DepartureTime: 29400 * 1e9,
	})
	require.NoError(t, err)

	api.GtfsManager.MockAddVehicleWithOptions("", tripID, routeID, internalgtfs.MockVehicleOptions{NoID: true})

	combinedStopID := utils.FormCombinedID(agencyID, stopID)
	resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
		arrivalsAndDeparturesURL(combinedStopID, "minutesBefore=60", "minutesAfter=60"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	var found bool
	for _, a := range model.Data.Entry.ArrivalsAndDepartures {
		_, arrTripID, _ := utils.ExtractAgencyIDAndCodeID(a.TripID)
		if arrTripID != tripID {
			continue
		}
		assert.Empty(t, a.VehicleID, "vehicleId should be empty for vehicle with nil ID")
		found = true
		break
	}
	assert.True(t, found, "should find arrival for test trip %s", tripID)
}
