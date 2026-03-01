package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

func TestArrivalsAndDeparturesForStopHandlerRequiresValidApiKey(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestArrivalsAndDeparturesForStopHandlerEndToEnd(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.NotZero(t, model.CurrentTime)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, entry)

	assert.Contains(t, entry, "arrivalsAndDepartures")
	assert.Contains(t, entry, "stopId")
	assert.Contains(t, entry, "nearbyStopIds")
	assert.Contains(t, entry, "situationIds")
	assert.Equal(t, stopID, entry["stopId"])

	arrivalsAndDepartures, ok := entry["arrivalsAndDepartures"].([]interface{})
	assert.True(t, ok)

	_, ok = entry["nearbyStopIds"].([]interface{})
	assert.True(t, ok)

	_, ok = entry["situationIds"].([]interface{})
	assert.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)
	assert.Contains(t, references, "agencies")

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, agencies)

	if len(arrivalsAndDepartures) > 0 {
		firstArrival, ok := arrivalsAndDepartures[0].(map[string]interface{})
		assert.True(t, ok)

		assert.Contains(t, firstArrival, "stopId")
		assert.Contains(t, firstArrival, "routeId")
		assert.Contains(t, firstArrival, "tripId")
		assert.Contains(t, firstArrival, "scheduledArrivalTime")
		assert.Contains(t, firstArrival, "scheduledDepartureTime")
		assert.Contains(t, firstArrival, "arrivalEnabled")
		assert.Contains(t, firstArrival, "departureEnabled")
		assert.Contains(t, firstArrival, "stopSequence")
		assert.Contains(t, firstArrival, "totalStopsInTrip")
		assert.Contains(t, firstArrival, "serviceDate")
		assert.Contains(t, firstArrival, "lastUpdateTime")
		assert.Contains(t, firstArrival, "vehicleId")
		assert.Contains(t, firstArrival, "predicted")
		assert.Contains(t, firstArrival, "distanceFromStop")
		assert.Contains(t, firstArrival, "numberOfStopsAway")
		assert.Contains(t, firstArrival, "tripHeadsign")
		assert.Contains(t, firstArrival, "routeShortName")
		assert.Contains(t, firstArrival, "routeLongName")

		if tripStatus, ok := firstArrival["tripStatus"].(map[string]interface{}); ok {
			assert.Contains(t, tripStatus, "activeTripId")
			assert.Contains(t, tripStatus, "blockTripSequence")
			assert.Contains(t, tripStatus, "closestStop")
			assert.Contains(t, tripStatus, "closestStopTimeOffset")
			assert.Contains(t, tripStatus, "distanceAlongTrip")
			assert.Contains(t, tripStatus, "phase")
			assert.Contains(t, tripStatus, "predicted")
			assert.Contains(t, tripStatus, "scheduleDeviation")
			assert.Contains(t, tripStatus, "serviceDate")
			assert.Contains(t, tripStatus, "status")
			assert.Contains(t, tripStatus, "vehicleId")

			if pos := tripStatus["position"]; pos != nil {
				position := pos.(map[string]interface{})
				assert.Contains(t, position, "lat")
				assert.Contains(t, position, "lon")
			}
		}

		assert.Equal(t, stopID, firstArrival["stopId"])
		assert.IsType(t, "", firstArrival["routeId"])
		assert.IsType(t, "", firstArrival["tripId"])
		assert.IsType(t, float64(0), firstArrival["scheduledArrivalTime"])
		assert.IsType(t, float64(0), firstArrival["scheduledDepartureTime"])
		assert.IsType(t, true, firstArrival["arrivalEnabled"])
		assert.IsType(t, true, firstArrival["departureEnabled"])
		assert.IsType(t, float64(0), firstArrival["stopSequence"])
		assert.IsType(t, float64(0), firstArrival["totalStopsInTrip"])
		assert.IsType(t, float64(0), firstArrival["serviceDate"])
		assert.IsType(t, float64(0), firstArrival["lastUpdateTime"])

		routes, ok := references["routes"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, routes)

		trips, ok := references["trips"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, trips)

		stops_ref, ok := references["stops"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, stops_ref)
	}
}

func TestArrivalsAndDeparturesForStopHandlerWithTimeParameters(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	minutesAfter := 60
	minutesBefore := 10

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST&minutesAfter="+
			strconv.Itoa(minutesAfter)+"&minutesBefore="+strconv.Itoa(minutesBefore))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, stopID, entry["stopId"])

	_, ok = entry["arrivalsAndDepartures"].([]interface{})
	assert.True(t, ok)

	_, ok = entry["nearbyStopIds"].([]interface{})
	assert.True(t, ok)

	_, ok = entry["situationIds"].([]interface{})
	assert.True(t, ok)

	_, ok = data["references"].(map[string]interface{})
	assert.True(t, ok)
}

func TestArrivalsAndDeparturesForStopHandlerWithSpecificTime(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	tomorrow := time.Now().AddDate(0, 0, 1)
	specificTime := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, time.Local)
	timeMs := specificTime.Unix() * 1000

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST&time="+strconv.FormatInt(timeMs, 10))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, stopID, entry["stopId"])

	assert.Contains(t, entry, "arrivalsAndDepartures")
	assert.Contains(t, entry, "nearbyStopIds")
	assert.Contains(t, entry, "situationIds")

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)
	assert.Contains(t, references, "agencies")
}

func TestArrivalsAndDeparturesForStopHandlerWithInvalidStopID(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	invalidStopID := utils.FormCombinedID(agency.Id, "invalid_stop")

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+invalidStopID+".json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestArrivalsAndDeparturesForStopHandlerWithMalformedStopID(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/arrivals-and-departures-for-stop/invalid_format.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestArrivalsAndDeparturesForStopHandlerNoActiveServices(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	futureTime := time.Now().AddDate(10, 0, 0)
	timeMs := futureTime.Unix() * 1000

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST&time="+strconv.FormatInt(timeMs, 10))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, stopID, entry["stopId"])

	arrivalsAndDepartures, ok := entry["arrivalsAndDepartures"].([]interface{})
	assert.True(t, ok)
	assert.Empty(t, arrivalsAndDepartures)

	_, ok = entry["nearbyStopIds"].([]interface{})
	assert.True(t, ok)

	_, ok = entry["situationIds"].([]interface{})
	assert.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, agencies)

	if routes, ok := references["routes"]; ok {
		if routeArray, ok := routes.([]interface{}); ok {
			assert.Empty(t, routeArray)
		}
	}
	if trips, ok := references["trips"]; ok {
		if tripArray, ok := trips.([]interface{}); ok {
			assert.Empty(t, tripArray)
		}
	}
}

func TestArrivalsAndDeparturesForStopHandlerDefaultParameters(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	assert.Contains(t, entry, "arrivalsAndDepartures")
	assert.Contains(t, entry, "stopId")
	assert.Contains(t, entry, "nearbyStopIds")
	assert.Contains(t, entry, "situationIds")

	assert.Equal(t, stopID, entry["stopId"])

	_, ok = entry["arrivalsAndDepartures"].([]interface{})
	assert.True(t, ok)

	_, ok = data["references"].(map[string]interface{})
	assert.True(t, ok)
}

func TestArrivalsAndDeparturesForStopHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/arrivals-and-departures-for-stop/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestParseArrivalsAndDeparturesParams_AllParameters(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test?minutesAfter=60&minutesBefore=15&time=1609459200000", nil)

	params, errs := api.parseArrivalsAndDeparturesParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 60, params.MinutesAfter)
	assert.Equal(t, 15, params.MinutesBefore)
	assert.False(t, params.Time.IsZero())
}

func TestParseArrivalsAndDeparturesParams_DefaultValues(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test", nil)

	params, errs := api.parseArrivalsAndDeparturesParams(req)

	assert.Nil(t, errs)
	assert.Equal(t, 35, params.MinutesAfter) // Default for plural handler
	assert.Equal(t, 5, params.MinutesBefore) // Default
	assert.WithinDuration(t, api.Clock.Now(), params.Time, 1*time.Second)
}

func TestParseArrivalsAndDeparturesParams_InvalidValues(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/test?minutesAfter=invalid&minutesBefore=invalid&time=invalid", nil)

	_, errs := api.parseArrivalsAndDeparturesParams(req)

	assert.NotNil(t, errs)
	assert.Contains(t, errs, "minutesAfter")
	assert.Contains(t, errs, "minutesBefore")
	assert.Contains(t, errs, "time")

	assert.Equal(t, "must be a valid integer", errs["minutesAfter"][0])
	assert.Equal(t, "must be a valid integer", errs["minutesBefore"][0])
	assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errs["time"][0])
}

func TestArrivalsAndDeparturesForStopHandlerWithInvalidParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	endpoint := "/api/where/arrivals-and-departures-for-stop/" + stopID + ".json?key=TEST&time=invalid"
	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	endpoint = "/api/where/arrivals-and-departures-for-stop/" + stopID + ".json?key=TEST&minutesAfter=invalid"
	resp, _ = serveApiAndRetrieveEndpoint(t, api, endpoint)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestArrivalsAndDeparturesForStopHandler_MultiAgency_Regression(t *testing.T) {
	// Use a MockClock within the service window so the plural handler finds the trip
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, loc))

	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	ctx := context.Background()
	queries := api.GtfsManager.GtfsDB.Queries

	agencyA := "AgencyA"
	stopID := "MultiAgencyStop"
	_, err = queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       agencyA,
		Name:     "Transit Agency A",
		Url:      "http://agency-a.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = queries.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:   stopID,
		Name: sql.NullString{String: "Shared Transit Center", Valid: true},
		Lat:  47.6062,
		Lon:  -122.3321,
	})
	require.NoError(t, err)

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
		ShortName: sql.NullString{String: "B-Line", Valid: true},
		LongName:  sql.NullString{String: "Agency B Express", Valid: true},
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
		StartDate: "20000101",
		EndDate:   "20301231",
	})
	require.NoError(t, err)

	tripB_ID := "TripB"
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:           tripB_ID,
		RouteID:      routeB_ID,
		ServiceID:    "service1",
		TripHeadsign: sql.NullString{String: "Downtown", Valid: true},
	})
	require.NoError(t, err)

	_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripB_ID,
		StopID:        stopID,
		StopSequence:  1,
		ArrivalTime:   28800 * 1e9, // 08:00:00 converted to nanoseconds
		DepartureTime: 29100 * 1e9, // 08:05:00 converted to nanoseconds
	})
	require.NoError(t, err)

	combinedStopID := utils.FormCombinedID(agencyA, stopID)

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+combinedStopID+".json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	// Verify arrivalsAndDepartures array
	arrivalsAndDepartures, ok := entry["arrivalsAndDepartures"].([]interface{})
	require.True(t, ok)

	// Fail loudly if no data is returned
	require.NotEmpty(t, arrivalsAndDepartures, "expected arrivals for multi-agency stop")

	firstArrival := arrivalsAndDepartures[0].(map[string]interface{})

	routeID, ok := firstArrival["routeId"].(string)
	require.True(t, ok)
	expectedRouteID := utils.FormCombinedID(agencyB, routeB_ID)
	assert.Equal(t, expectedRouteID, routeID,
		"routeId should use the route's agency (AgencyB), not the stop's agency (AgencyA)")

	// Verify references contain both agencies
	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := references["agencies"].([]interface{})
	require.True(t, ok)

	agencyIDs := make(map[string]bool)
	for _, ag := range agencies {
		agencyMap := ag.(map[string]interface{})
		agencyIDs[agencyMap["id"].(string)] = true
	}

	assert.True(t, agencyIDs[agencyA], "references.agencies should contain Agency A")
	assert.True(t, agencyIDs[agencyB], "references.agencies should contain Agency B")

	// Verify route is correctly prefixed
	routes, ok := references["routes"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, routes, "references.routes should not be empty")

	foundCorrectRoute := false
	for _, r := range routes {
		routeMap := r.(map[string]interface{})
		if routeMap["id"].(string) == expectedRouteID {
			foundCorrectRoute = true
			assert.Equal(t, agencyB, routeMap["agencyId"], "route's agencyId should be AgencyB")
			break
		}
	}
	assert.True(t, foundCorrectRoute, "references.routes should contain the correctly prefixed route")
}

func TestArrivalsAndDeparturesReturnsResultsNearMidnight(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 13, 11, 0, 0, 0, time.UTC))

	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	var foundResults bool

	for _, stop := range stops {
		stopID := utils.FormCombinedID(agency.Id, stop.Id)
		url := "/api/where/arrivals-and-departures-for-stop/" + stopID + ".json?key=TEST&minutesBefore=15&minutesAfter=240"

		resp, model := serveApiAndRetrieveEndpoint(t, api, url)

		if resp.StatusCode == http.StatusOK {
			if data, ok := model.Data.(map[string]interface{}); ok {
				if entry, ok := data["entry"].(map[string]interface{}); ok {
					if arrivals, ok := entry["arrivalsAndDepartures"].([]interface{}); ok && len(arrivals) > 0 {
						foundResults = true
						break
					}
				}
			}
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
		ID:       agencyID,
		Name:     "Delay Prop Agency",
		Url:      "http://example.com",
		Timezone: "UTC",
	})
	require.NoError(t, err)

	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:   stopCode,
		Name: sql.NullString{String: "Delay Test Stop", Valid: true},
		Lat:  47.0,
		Lon:  -122.0,
	})
	require.NoError(t, err)

	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID:        routeID,
		AgencyID:  agencyID,
		ShortName: sql.NullString{String: "DT", Valid: true},
		LongName:  sql.NullString{String: "Delay Test Route", Valid: true},
		Type:      3,
	})
	require.NoError(t, err)

	// 2010-01-01 is a Friday; cover all days to keep setup simple.
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:        serviceID,
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20100101",
		EndDate:   "20301231",
	})
	require.NoError(t, err)

	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:        tripID,
		RouteID:   routeID,
		ServiceID: serviceID,
		BlockID:   sql.NullString{String: "dp-block", Valid: true},
	})
	require.NoError(t, err)

	arrivalNanos := int64(28800) * 1e9 // 08:00:00 as nanoseconds since midnight
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID:        tripID,
		StopID:        stopCode,
		StopSequence:  stopSeq,
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
			StopID:       &stopCode,
			StopSequence: &seq,
			Arrival:      &gtfs.StopTimeEvent{Delay: &delay},
		},
	})

	_, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+combinedStopID+".json?key=TEST")

	data := model.Data.(map[string]interface{})
	entry := data["entry"].(map[string]interface{})
	arrivals := entry["arrivalsAndDepartures"].([]interface{})
	require.NotEmpty(t, arrivals, "expected at least one arrival")

	a := arrivals[0].(map[string]interface{})
	assert.True(t, a["predicted"].(bool), "exact stop match should be predicted")
	assert.Equal(t, float64(scheduledArrivalMs+60000), a["predictedArrivalTime"],
		"predicted arrival should be scheduled + 60s")
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
		{
			StopSequence: &priorSeq,
			Arrival:      &gtfs.StopTimeEvent{Delay: &delay},
		},
	})

	_, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+combinedStopID+".json?key=TEST")

	data := model.Data.(map[string]interface{})
	entry := data["entry"].(map[string]interface{})
	arrivals := entry["arrivalsAndDepartures"].([]interface{})
	require.NotEmpty(t, arrivals, "expected at least one arrival")

	a := arrivals[0].(map[string]interface{})
	assert.True(t, a["predicted"].(bool), "should be predicted via prior stop propagation")
	assert.Equal(t, float64(scheduledArrivalMs+90000), a["predictedArrivalTime"],
		"predicted arrival should be scheduled + propagated 90s delay")
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

	_, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+combinedStopID+".json?key=TEST")

	data := model.Data.(map[string]interface{})
	entry := data["entry"].(map[string]interface{})
	arrivals := entry["arrivalsAndDepartures"].([]interface{})
	require.NotEmpty(t, arrivals, "expected at least one arrival")

	a := arrivals[0].(map[string]interface{})
	assert.True(t, a["predicted"].(bool), "trip-level delay should mark arrival as predicted")
	assert.Equal(t, float64(scheduledArrivalMs+120000), a["predictedArrivalTime"],
		"predicted arrival should be scheduled + trip-level 120s delay")
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
		{
			StopSequence: &laterSeq,
			Arrival:      &gtfs.StopTimeEvent{Delay: &delay},
		},
	})

	_, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+combinedStopID+".json?key=TEST")

	data := model.Data.(map[string]interface{})
	entry := data["entry"].(map[string]interface{})
	arrivals := entry["arrivalsAndDepartures"].([]interface{})
	require.NotEmpty(t, arrivals, "expected at least one arrival")

	a := arrivals[0].(map[string]interface{})
	assert.False(t, a["predicted"].(bool), "update for a later stop should not predict current stop")
	assert.Equal(t, float64(0), a["predictedArrivalTime"],
		"predictedArrivalTime should be 0 when not predicted")
}

// TestPluralArrivals_VehiclePositionAloneDoesNotPredict verifies that a vehicle
// position without any TripUpdate does NOT mark the arrival as predicted.
// This tests the behavior change from the old code which incorrectly set predicted=true
// whenever a vehicle existed, even without real-time delay data.
func TestPluralArrivals_VehiclePositionAloneDoesNotPredict(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2010, 1, 1, 8, 2, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)

	// Add a vehicle but no trip update.
	api.GtfsManager.MockAddVehicle("v1", tripID, "dp-route")

	_, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-stop/"+combinedStopID+".json?key=TEST")

	data := model.Data.(map[string]interface{})
	entry := data["entry"].(map[string]interface{})
	arrivals := entry["arrivalsAndDepartures"].([]interface{})
	require.NotEmpty(t, arrivals, "expected at least one arrival")

	a := arrivals[0].(map[string]interface{})
	assert.False(t, a["predicted"].(bool),
		"vehicle position alone should not mark arrival as predicted")
	assert.Equal(t, float64(0), a["predictedArrivalTime"],
		"predictedArrivalTime should be 0 when only vehicle position is present")
}
