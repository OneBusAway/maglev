package restapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
