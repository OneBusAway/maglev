package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// --- Param parsing unit tests ---

func TestParseArrivalsAndDeparturesForLocationParams_Defaults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008", nil)
	params, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 47.653, params.Lat)
	assert.Equal(t, -122.307, params.Lon)
	assert.Equal(t, 0.008, params.LatSpan)
	assert.Equal(t, 0.008, params.LonSpan)
	assert.Equal(t, 5, params.MinutesBefore)
	assert.Equal(t, 35, params.MinutesAfter)
	assert.Equal(t, 250, params.MaxCount)
	assert.WithinDuration(t, api.Clock.Now(), params.Time, time.Second)
}

func TestParseArrivalsAndDeparturesForLocationParams_CustomValues(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&radius=500&minutesBefore=10&minutesAfter=60&maxCount=50&time=1609459200000", nil)
	params, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 47.653, params.Lat)
	assert.Equal(t, -122.307, params.Lon)
	assert.Equal(t, 500.0, params.Radius)
	assert.Equal(t, 10, params.MinutesBefore)
	assert.Equal(t, 60, params.MinutesAfter)
	assert.Equal(t, 50, params.MaxCount)
	assert.False(t, params.Time.IsZero())
}

func TestParseArrivalsAndDeparturesForLocationParams_MissingLatLon(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test", nil)
	_, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "lat")
	assert.Contains(t, errs, "lon")
}

func TestParseArrivalsAndDeparturesForLocationParams_InvalidTime(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008&time=notanumber", nil)
	_, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "time")
	assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errs["time"][0])
}

func TestParseArrivalsAndDeparturesForLocationParams_InvalidMinutesAfter(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008&minutesAfter=notanumber", nil)
	_, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "minutesAfter")
	assert.Equal(t, "must be a valid integer", errs["minutesAfter"][0])
}

func TestParseArrivalsAndDeparturesForLocationParams_InvalidMinutesBefore(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008&minutesBefore=notanumber", nil)
	_, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "minutesBefore")
	assert.Equal(t, "must be a valid integer", errs["minutesBefore"][0])
}

func TestParseArrivalsAndDeparturesForLocationParams_NegativeMinutes(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008&minutesBefore=-1&minutesAfter=-5", nil)
	_, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "minutesBefore")
	assert.Contains(t, errs, "minutesAfter")
}

func TestParseArrivalsAndDeparturesForLocationParams_MinutesCappedAtMax(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008&minutesBefore=9999&minutesAfter=9999", nil)
	params, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 60, params.MinutesBefore)
	assert.Equal(t, 240, params.MinutesAfter)
}

func TestParseArrivalsAndDeparturesForLocationParams_InvalidMaxCount(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET",
		"/test?lat=47.653&lon=-122.307&latSpan=0.008&lonSpan=0.008&maxCount=0", nil)
	_, errs := api.parseArrivalsAndDeparturesForLocationParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "maxCount")
}

// --- HTTP handler integration tests ---

func TestArrivalsAndDeparturesForLocationRequiresValidAPIKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrivals-and-departures-for-location.json?key=invalid&lat=40.583321&lon=-122.426966&latSpan=0.01&lonSpan=0.01")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestArrivalsAndDeparturesForLocationMissingLatLon(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestArrivalsAndDeparturesForLocationInvalidTime(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.01&lonSpan=0.01&time=notanumber")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestArrivalsAndDeparturesForLocationEmptyAreaReturnsOK(t *testing.T) {
	// Coordinates far from any test GTFS data so no stops are found.
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=0.0&lon=0.0&latSpan=0.001&lonSpan=0.001")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.NotZero(t, model.CurrentTime)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	// Entry must contain all expected keys even when empty.
	assert.Contains(t, entry, "arrivalsAndDepartures")
	assert.Contains(t, entry, "stopIds")
	assert.Contains(t, entry, "nearbyStopIds")
	assert.Contains(t, entry, "situationIds")
	assert.Contains(t, entry, "limitExceeded")

	ads, ok := entry["arrivalsAndDepartures"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, ads)

	stopIDs, ok := entry["stopIds"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, stopIDs)

	assert.False(t, entry["limitExceeded"].(bool))
}

func TestArrivalsAndDeparturesForLocationEndToEnd(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.NotZero(t, model.CurrentTime)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	// Required entry keys.
	assert.Contains(t, entry, "arrivalsAndDepartures")
	assert.Contains(t, entry, "stopIds")
	assert.Contains(t, entry, "nearbyStopIds")
	assert.Contains(t, entry, "situationIds")
	assert.Contains(t, entry, "limitExceeded")

	// nearbyStopIds must be a list of objects with stopId + distanceFromQuery.
	nearbyRaw, ok := entry["nearbyStopIds"].([]interface{})
	require.True(t, ok, "nearbyStopIds should be a list")
	for _, item := range nearbyRaw {
		nearby, ok := item.(map[string]interface{})
		require.True(t, ok, "each nearbyStopIds entry should be an object")
		assert.Contains(t, nearby, "stopId")
		assert.Contains(t, nearby, "distanceFromQuery")
	}

	// stopIds must be a list.
	stopIDs, ok := entry["stopIds"].([]interface{})
	require.True(t, ok, "stopIds should be a list")
	assert.NotEmpty(t, stopIDs, "should have found stops in this area")

	// References block.
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "references should be a map")
	assert.Contains(t, refs, "agencies")
	assert.Contains(t, refs, "routes")
	assert.Contains(t, refs, "stops")
	assert.Contains(t, refs, "trips")
	assert.Contains(t, refs, "situations")

	// Validate arrival shape if any were returned.
	ads, ok := entry["arrivalsAndDepartures"].([]interface{})
	require.True(t, ok, "arrivalsAndDepartures should be a list")

	if len(ads) == 0 {
		t.Skip("no arrivals in test data for this time/location")
	}

	ad, ok := ads[0].(map[string]interface{})
	require.True(t, ok, "first arrival should be a map")

	// Required arrival fields.
	for _, field := range []string{
		"routeId", "tripId", "stopId", "serviceDate",
		"scheduledArrivalTime", "scheduledDepartureTime",
		"predictedArrivalTime", "predictedDepartureTime",
		"predicted", "status", "situationIds",
		"routeShortName", "tripHeadsign",
		"arrivalEnabled", "departureEnabled",
		"numberOfStopsAway", "distanceFromStop",
		"blockTripSequence", "totalStopsInTrip",
		"frequency",
	} {
		assert.Contains(t, ad, field, "arrival must contain field %q", field)
	}

	assert.Equal(t, "default", ad["status"])

	// Every arrival's stopId must be one of the queried stopIds.
	stopIDInAD, _ := ad["stopId"].(string)
	assert.NotEmpty(t, stopIDInAD)
	assert.Contains(t, stopIDs, stopIDInAD,
		"arrival stopId should be one of the queried stopIds")
}

func TestArrivalsAndDeparturesForLocationStopIdsOnlyContainsStopsWithArrivals(t *testing.T) {
	// Java only includes a stop in stopIds when it has at least one arrival.
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	ads, _ := entry["arrivalsAndDepartures"].([]interface{})
	stopIDs, _ := entry["stopIds"].([]interface{})

	if len(ads) == 0 {
		// No arrivals → stopIds must also be empty.
		assert.Empty(t, stopIDs, "stopIds must be empty when there are no arrivals")
		return
	}

	// Every stopId in the entry must appear in at least one arrival's stopId field.
	arrivalStopIDs := make(map[interface{}]bool)
	for _, adRaw := range ads {
		if ad, ok := adRaw.(map[string]interface{}); ok {
			arrivalStopIDs[ad["stopId"]] = true
		}
	}
	for _, sid := range stopIDs {
		assert.True(t, arrivalStopIDs[sid],
			"stopId %v in entry.stopIds has no matching arrival", sid)
	}
}

func TestArrivalsAndDeparturesForLocationWithLatSpanLonSpan(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.045&lonSpan=0.059")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	assert.Contains(t, entry, "stopIds")
	assert.Contains(t, entry, "arrivalsAndDepartures")
	assert.Contains(t, entry, "limitExceeded")
}

func TestArrivalsAndDeparturesForLocationReferencesConsistency(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	ads, _ := entry["arrivalsAndDepartures"].([]interface{})
	if len(ads) == 0 {
		t.Skip("no arrivals in test data for this location")
	}

	routeRefs, _ := refs["routes"].([]interface{})
	tripRefs, _ := refs["trips"].([]interface{})
	agencies, _ := refs["agencies"].([]interface{})

	routeRefIDs := collectAllIdsFromObjects(t, routeRefs, "id")
	tripRefIDs := collectAllIdsFromObjects(t, tripRefs, "id")
	agencyRefIDs := collectAllIdsFromObjects(t, agencies, "id")

	// Every arrival's routeId and tripId must appear in references.
	for _, adRaw := range ads {
		ad, ok := adRaw.(map[string]interface{})
		require.True(t, ok)

		routeID, _ := ad["routeId"].(string)
		assert.Contains(t, routeRefIDs, routeID,
			"every arrival routeId must appear in references.routes")

		tripID, _ := ad["tripId"].(string)
		assert.Contains(t, tripRefIDs, tripID,
			"every arrival tripId must appear in references.trips")
	}

	// Every route's agencyId must appear in references.agencies.
	agencyIDsFromRoutes := collectAllIdsFromObjects(t, routeRefs, "agencyId")
	for _, aid := range agencyIDsFromRoutes {
		assert.Contains(t, agencyRefIDs, aid,
			"every route agencyId must appear in references.agencies")
	}
}

func TestArrivalsAndDeparturesForLocationArrivalsAreSortedByTime(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	ads, _ := entry["arrivalsAndDepartures"].([]interface{})
	if len(ads) < 2 {
		t.Skip("need at least 2 arrivals to test sort order")
	}

	var prevTime float64
	for i, adRaw := range ads {
		ad, ok := adRaw.(map[string]interface{})
		require.True(t, ok)

		predicted, _ := ad["predicted"].(bool)
		var arrTime float64
		if predicted {
			arrTime, _ = ad["predictedArrivalTime"].(float64)
		}
		if arrTime == 0 {
			arrTime, _ = ad["scheduledArrivalTime"].(float64)
		}

		if i > 0 {
			assert.GreaterOrEqual(t, arrTime, prevTime,
				"arrivals must be sorted ascending by arrival time (index %d)", i)
		}
		prevTime = arrTime
	}
}

func TestArrivalsAndDeparturesForLocationLimitExceeded(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	// maxCount=1 forces limitExceeded=true if there is more than 1 arrival.
	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500&maxCount=1")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	ads, _ := entry["arrivalsAndDepartures"].([]interface{})
	assert.LessOrEqual(t, len(ads), 1)
}
