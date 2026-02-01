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

func TestTripDetailsHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/invalid.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestTripDetailsHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/"+tripID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	assert.Equal(t, tripID, entry["tripId"])
	assert.NotNil(t, entry["serviceDate"])

	// Testing Default Path where no Service Date is given
	loc, err := time.LoadLocation(agency.Timezone)
	assert.Nil(t, err)

	currentTimeInLoc := time.Now().In(loc)
	y, m, d := currentTimeInLoc.Date()
	expectedServiceDate := time.Date(y, m, d, 0, 0, 0, 0, loc)
	expectedServiceDateMillis := expectedServiceDate.Unix() * 1000
	assert.Equal(t, float64(expectedServiceDateMillis), entry["serviceDate"])

	// Test if the fields are being omitted on empty or not
	_, exists := entry["situationIds"]
	assert.True(t, exists)
	_, exists = entry["frequency"]
	assert.True(t, exists)

	schedule, ok := entry["schedule"].(map[string]interface{})
	if ok {
		assert.NotNil(t, schedule)

		stopTimes, stopTimesOk := schedule["stopTimes"].([]interface{})
		if stopTimesOk {
			assert.GreaterOrEqual(t, len(stopTimes), 0)

			if len(stopTimes) > 0 {
				stopTime, ok := stopTimes[0].(map[string]interface{})
				assert.True(t, ok)
				assert.NotNil(t, stopTime["stopId"])
				assert.NotNil(t, stopTime["arrivalTime"])
				assert.NotNil(t, stopTime["departureTime"])
			}
		}

		assert.NotNil(t, schedule["timeZone"])
	}

	// Test status section (if includeStatus=true by default)
	status, statusOk := entry["status"].(map[string]interface{})
	if statusOk {
		assert.NotNil(t, status)
		assert.NotNil(t, status["serviceDate"])
		assert.Contains(t, []interface{}{"scheduled", "in_progress", "completed"}, status["phase"])
		assert.NotNil(t, status["predicted"])
	}

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok, "References section should exist")
	assert.NotNil(t, references, "References should not be nil")

	// Test trip references (if includeTrip=true by default)
	tripsRef, tripsOk := references["trips"].([]interface{})
	if tripsOk {
		assert.NotEmpty(t, tripsRef, "Trips should not be empty")

		trip, ok := tripsRef[0].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, tripID, trip["id"])
		assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].Route.Id), trip["routeId"])
		assert.Equal(t, utils.FormCombinedID(agency.Id, trips[0].Service.Id), trip["serviceId"])
	}

	routes, ok := references["routes"].([]interface{})
	assert.True(t, ok, "Routes section should exist in references")
	assert.NotEmpty(t, routes, "Routes should not be empty")

	assert.True(t, ok)
	assert.NotEmpty(t, routes)

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "Agencies section should exist in references")
	assert.NotEmpty(t, agencies, "Agencies should not be empty")

	agencyRef, ok := agencies[0].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, agency.Id, agencyRef["id"])
	assert.Equal(t, agency.Name, agencyRef["name"])

	// Test stop references (should exist if schedule is included)
	stops, stopsOk := references["stops"].([]interface{})
	if stopsOk && len(stops) > 0 {
		stop, ok := stops[0].(map[string]interface{})
		assert.True(t, ok)
		assert.NotNil(t, stop["id"])
		assert.NotNil(t, stop["name"])
		assert.NotNil(t, stop["lat"])
		assert.NotNil(t, stop["lon"])
	}
}

func TestTripDetailsHandlerWithInvalidTripID(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/agency_invalid.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestTripDetailsHandlerWithServiceDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Use tomorrow's date as service date
	tomorrow := time.Now().AddDate(0, 0, 1)
	serviceDateMs := tomorrow.Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/trip-details/"+tripID+".json?key=TEST&serviceDate="+
			strconv.FormatInt(serviceDateMs, 10))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, float64(serviceDateMs), entry["serviceDate"])
}

func TestTripDetailsHandlerWithIncludeTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Test with includeTrip=false
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/"+tripID+".json?key=TEST&includeTrip=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)

	// When includeTrip=false, trips section should be empty or not exist
	trips_ref, tripsOk := references["trips"]
	if tripsOk {
		tripsArray, ok := trips_ref.([]interface{})
		if ok {
			assert.Empty(t, tripsArray, "Trips should be empty when includeTrip=false")
		}
	}
}

func TestTripDetailsHandlerWithIncludeSchedule(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Test with includeSchedule=false
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/"+tripID+".json?key=TEST&includeSchedule=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	// When includeSchedule=false, schedule should be nil or not exist
	schedule, scheduleExists := entry["schedule"]
	if scheduleExists {
		assert.Nil(t, schedule, "Schedule should be nil when includeSchedule=false")
	}

	// Stops should also not be included in references
	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)

	stops, stopsOk := references["stops"]
	if stopsOk {
		stopsArray, ok := stops.([]interface{})
		if ok {
			assert.Empty(t, stopsArray, "Stops should be empty when includeSchedule=false")
		}
	}
}

func TestTripDetailsHandlerWithIncludeStatus(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Test with includeStatus=false
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-details/"+tripID+".json?key=TEST&includeStatus=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	// When includeStatus=false, status should be nil or not exist
	status, statusExists := entry["status"]
	if statusExists {
		assert.Nil(t, status, "Status should be nil when includeStatus=false")
	}
}

func TestTripDetailsHandlerWithTimeParameter(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Use a specific time (1 hour from now)
	specificTime := time.Now().Add(1 * time.Hour)
	timeMs := specificTime.Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/trip-details/"+tripID+".json?key=TEST&time="+strconv.FormatInt(timeMs, 10))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	// The response should be successful (time parameter affects internal calculations)
	assert.NotNil(t, entry["tripId"])
}

func TestTripDetailsHandlerWithAllParametersFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/trip-details/"+tripID+".json?key=TEST&includeTrip=false&includeSchedule=false&includeStatus=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	// Basic fields should still exist
	assert.Equal(t, tripID, entry["tripId"])
	assert.NotNil(t, entry["serviceDate"])

	// Optional sections should be nil or empty
	schedule, scheduleExists := entry["schedule"]
	if scheduleExists {
		assert.Nil(t, schedule)
	}

	status, statusExists := entry["status"]
	if statusExists {
		assert.Nil(t, status)
	}

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)

	// Should still have route and agency references, but not trips or stops
	routes, ok := references["routes"].([]interface{})
	assert.True(t, ok)
	assert.Empty(t, routes)

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, agencies)
}

func TestTripDetailsHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/trip-details/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestTripDetailsHandlerWithInvalidParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	endpoint := "/api/where/trip-details/" + tripID + ".json?key=TEST&serviceDate=invalid"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	endpoint = "/api/where/trip-details/" + tripID + ".json?key=TEST&time=invalid"

	resp2, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

func TestParseTripIdDetailsParams_Unit(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/?includeTrip=false&includeSchedule=false&serviceDate=1609459200000", nil)
	params, errs := api.parseTripIdDetailsParams(req)

	assert.Nil(t, errs)
	assert.False(t, params.IncludeTrip)
	assert.False(t, params.IncludeSchedule)
	assert.NotNil(t, params.ServiceDate)

	reqDefault := httptest.NewRequest("GET", "/", nil)
	paramsDefault, errsDefault := api.parseTripIdDetailsParams(reqDefault)

	assert.Nil(t, errsDefault)
	assert.True(t, paramsDefault.IncludeTrip)
	assert.True(t, paramsDefault.IncludeStatus)
	assert.True(t, paramsDefault.IncludeSchedule)

	reqInvalid := httptest.NewRequest("GET", "/?time=invalid&serviceDate=invalid", nil)
	_, errsInvalid := api.parseTripIdDetailsParams(reqInvalid)

	assert.NotNil(t, errsInvalid)
	assert.Contains(t, errsInvalid, "time")
	assert.Contains(t, errsInvalid, "serviceDate")
	assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errsInvalid["time"][0])
}
