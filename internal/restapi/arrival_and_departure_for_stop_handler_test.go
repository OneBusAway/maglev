package restapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func TestArrivalAndDepartureForStopHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=invalid&tripId="+tripID+"&serviceDate="+
			fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestArrivalAndDepartureForStopHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/arrival-and-departure-for-stop/" + stopID +
		".json?key=TEST&tripId=" + tripID + "&serviceDate=" + fmt.Sprintf("%d", serviceDate))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	// The response might be 404 if the trip doesn't serve this stop, which is acceptable
	switch resp.StatusCode {
	case http.StatusOK:
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, data)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		// Verify entry fields
		assert.Equal(t, stopID, entry["stopId"])
		assert.Equal(t, tripID, entry["tripId"])
		assert.Equal(t, float64(serviceDate), entry["serviceDate"])
		assert.NotNil(t, entry["scheduledArrivalTime"])
		assert.NotNil(t, entry["scheduledDepartureTime"])
		assert.NotNil(t, entry["arrivalEnabled"])
		assert.NotNil(t, entry["departureEnabled"])
		assert.NotNil(t, entry["stopSequence"])
		assert.NotNil(t, entry["totalStopsInTrip"])

		// Verify references
		references, ok := data["references"].(map[string]interface{})
		assert.True(t, ok)
		assert.NotNil(t, references)

		agencies, ok := references["agencies"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, agencies)

		routes, ok := references["routes"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, routes)

		trips_ref, ok := references["trips"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, trips_ref)

		stops_ref, ok := references["stops"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, stops_ref)
	case http.StatusNotFound:
		// This is acceptable if the trip doesn't serve this stop
		assert.Equal(t, http.StatusNotFound, model.Code)
	default:
		t.Fatalf("Unexpected status code: %d", resp.StatusCode)
	}
}

func TestArrivalAndDepartureForStopHandlerWithInvalidStopID(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/agency_invalid.json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestArrivalAndDepartureForStopHandlerWithTimeParameter(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Use a specific time (1 hour from now)
	specificTime := time.Now().Add(1 * time.Hour)
	timeMs := specificTime.Unix() * 1000
	serviceDate := specificTime.Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate)+"&time="+strconv.FormatInt(timeMs, 10))

	// The response might be 404 if the trip doesn't serve this stop, which is acceptable
	switch resp.StatusCode {
	case http.StatusOK:
		assert.Equal(t, http.StatusOK, model.Code)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		// The response should be successful
		assert.Equal(t, stopID, entry["stopId"])
		assert.Equal(t, tripID, entry["tripId"])
	case http.StatusNotFound:
		// This is acceptable if the trip doesn't serve this stop
		assert.Equal(t, http.StatusNotFound, model.Code)
	}
}

func TestArrivalAndDepartureForStopHandlerRequiresTripId(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	serviceDate := time.Now().Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/arrival-and-departure-for-stop/" + stopID +
		".json?key=TEST&serviceDate=" + fmt.Sprintf("%d", serviceDate))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResponse struct {
		FieldErrors map[string][]string `json:"fieldErrors"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	assert.Contains(t, errorResponse.FieldErrors, "tripId")
	assert.Len(t, errorResponse.FieldErrors["tripId"], 1)
	assert.Equal(t, "missingRequiredField", errorResponse.FieldErrors["tripId"][0])
}

func TestArrivalAndDepartureForStopHandlerRequiresServiceDate(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/arrival-and-departure-for-stop/" + stopID +
		".json?key=TEST&tripId=" + tripID)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResponse struct {
		FieldErrors map[string][]string `json:"fieldErrors"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	assert.Contains(t, errorResponse.FieldErrors, "serviceDate")
	assert.Len(t, errorResponse.FieldErrors["serviceDate"], 1)
	assert.Equal(t, "missingRequiredField", errorResponse.FieldErrors["serviceDate"][0])
}

func TestArrivalAndDepartureForStopHandlerWithStopSequence(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000
	stopSequence := 1

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate)+"&stopSequence="+strconv.Itoa(stopSequence))

	// The response might be 404 if the stop sequence doesn't match, which is acceptable
	switch resp.StatusCode {
	case http.StatusOK:
		assert.Equal(t, http.StatusOK, model.Code)
		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)
		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, stopID, entry["stopId"])
	case http.StatusNotFound:
		assert.Equal(t, http.StatusNotFound, model.Code)
	}
}

func TestArrivalAndDepartureForStopHandlerWithMinutesParameters(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate)+"&minutesBefore=10&minutesAfter=60")

	// The response might be 404 if the trip doesn't serve this stop
	switch resp.StatusCode {
	case http.StatusOK:
		assert.Equal(t, http.StatusOK, model.Code)
	case http.StatusNotFound:
		assert.Equal(t, http.StatusNotFound, model.Code)
	}
}

func TestArrivalAndDepartureForStopHandlerWithInvalidTripID(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, "nonexistent_trip")
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestArrivalAndDepartureForStopHandlerWithMalformedTripID(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := "malformedid" // No underscore, will fail extraction
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, model.Code)
}

func TestArrivalAndDepartureForStopHandlerWithMalformedStopID(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	stopID := "malformedid" // No underscore, will fail extraction
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, model.Code)
}

func TestArrivalAndDepartureForStopHandlerWithValidTripStopCombination(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	ctx := context.Background()

	// Find a valid trip with stop times
	trips := api.GtfsManager.GetTrips()
	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	var validTripID, validStopID string
	var stopSequence int64

	// Search for a trip that has stop times
	for _, trip := range trips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if err == nil && len(stopTimes) > 0 {
			validTripID = trip.ID
			validStopID = stopTimes[0].StopID
			stopSequence = stopTimes[0].StopSequence
			break
		}
	}

	if validTripID == "" {
		t.Skip("No valid trip-stop combinations found in test data")
	}

	combinedStopID := utils.FormCombinedID(agency.Id, validStopID)
	combinedTripID := utils.FormCombinedID(agency.Id, validTripID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+combinedStopID+".json?key=TEST&tripId="+combinedTripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	// Verify all the important fields
	assert.Equal(t, combinedStopID, entry["stopId"])
	assert.Equal(t, combinedTripID, entry["tripId"])
	assert.Equal(t, float64(serviceDate), entry["serviceDate"])
	assert.NotNil(t, entry["scheduledArrivalTime"])
	assert.NotNil(t, entry["scheduledDepartureTime"])
	assert.Equal(t, true, entry["arrivalEnabled"])
	assert.Equal(t, true, entry["departureEnabled"])
	assert.Equal(t, float64(stopSequence-1), entry["stopSequence"]) // Zero-based
	assert.NotNil(t, entry["totalStopsInTrip"])

	// Verify references
	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := references["agencies"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, agencies)

	routes, ok := references["routes"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, routes)

	trips_ref, ok := references["trips"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, trips_ref)

	stops_ref, ok := references["stops"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, stops_ref)
}

func TestArrivalAndDepartureForStopHandlerWithValidTripAndStopSequence(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	ctx := context.Background()

	// Find a valid trip with multiple stops
	trips := api.GtfsManager.GetTrips()
	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	var validTripID, validStopID string
	var stopSequence int64

	// Search for a trip that has at least 2 stop times
	for _, trip := range trips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if err == nil && len(stopTimes) >= 2 {
			validTripID = trip.ID
			validStopID = stopTimes[1].StopID // Use second stop
			stopSequence = stopTimes[1].StopSequence
			break
		}
	}

	if validTripID == "" {
		t.Skip("No valid trip with multiple stops found in test data")
	}

	combinedStopID := utils.FormCombinedID(agency.Id, validStopID)
	combinedTripID := utils.FormCombinedID(agency.Id, validTripID)
	serviceDate := time.Now().Unix() * 1000

	// Test with correct stop sequence
	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+combinedStopID+".json?key=TEST&tripId="+combinedTripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate)+"&stopSequence="+strconv.FormatInt(stopSequence, 10))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, float64(stopSequence-1), entry["stopSequence"]) // Zero-based

	// Test with wrong stop sequence (should return 404)
	wrongSequence := stopSequence + 100
	_, resp2, model2 := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+combinedStopID+".json?key=TEST&tripId="+combinedTripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate)+"&stopSequence="+strconv.FormatInt(wrongSequence, 10))

	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
	assert.Equal(t, http.StatusNotFound, model2.Code)
}

func TestGetPredictedTimes_NoRealTimeData(t *testing.T) {
	api := createTestApi(t)

	scheduledArrival := time.Now()
	scheduledDeparture := scheduledArrival.Add(2 * time.Minute)

	// When there's no real-time data, should return 0, 0
	predArrival, predDeparture := api.getPredictedTimes("nonexistent_trip", "nonexistent_stop", scheduledArrival, scheduledDeparture)

	assert.Equal(t, int64(0), predArrival)
	assert.Equal(t, int64(0), predDeparture)
}

func TestGetPredictedTimes_EqualArrivalDeparture(t *testing.T) {
	api := createTestApi(t)

	// Test the case where scheduled arrival == scheduled departure
	scheduledTime := time.Now()

	// Even without real-time data, test the logic path
	// This tests that the function handles the case correctly
	predArrival, predDeparture := api.getPredictedTimes("test_trip", "test_stop", scheduledTime, scheduledTime)

	// Without real-time data, returns 0,0
	assert.Equal(t, int64(0), predArrival)
	assert.Equal(t, int64(0), predDeparture)
}

func TestGetRemainingDistanceToStop_NilVehicle(t *testing.T) {
	api := createTestApi(t)
	ctx := context.Background()

	result := api.getBlockDistanceToStop(ctx, "test_trip", "test_stop", nil, time.Now())

	assert.Nil(t, result)
}

func TestGetRemainingDistanceToStop_NoPosition(t *testing.T) {
	api := createTestApi(t)
	ctx := context.Background()

	vehicle := &gtfs.Vehicle{
		Position: nil,
	}

	result := api.getBlockDistanceToStop(ctx, "test_trip", "test_stop", vehicle, time.Now())

	assert.Nil(t, result)
}

func TestGetNumberOfStopsAway_NilCurrentSequence(t *testing.T) {
	api := createTestApi(t)
	vehicle := &gtfs.Vehicle{
		// No current stop sequence set
	}

	result := api.getNumberOfStopsAway(context.Background(), "test_trip", 5, vehicle, time.Now())

	assert.Nil(t, result)
}

func TestParseArrivalAndDepartureParams_AllParameters(t *testing.T) {
	api := createTestApi(t)

	req := httptest.NewRequest("GET", "/test?minutesAfter=60&minutesBefore=15&time=1609459200000&tripId=trip_123&serviceDate=1609459200000&vehicleId=vehicle_456&stopSequence=3", nil)

	params := api.parseArrivalAndDepartureParams(req)

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

	req := httptest.NewRequest("GET", "/test", nil)

	params := api.parseArrivalAndDepartureParams(req)

	assert.Equal(t, 30, params.MinutesAfter) // Default
	assert.Equal(t, 5, params.MinutesBefore) // Default
	assert.Nil(t, params.Time)
	assert.Equal(t, "", params.TripID)
	assert.Nil(t, params.ServiceDate)
	assert.Equal(t, "", params.VehicleID)
	assert.Nil(t, params.StopSequence)
}

func TestParseArrivalAndDepartureParams_InvalidValues(t *testing.T) {
	api := createTestApi(t)

	req := httptest.NewRequest("GET", "/test?minutesAfter=invalid&minutesBefore=invalid&time=invalid&serviceDate=invalid&stopSequence=invalid", nil)

	params := api.parseArrivalAndDepartureParams(req)

	// Should use defaults when parsing fails
	assert.Equal(t, 30, params.MinutesAfter)
	assert.Equal(t, 5, params.MinutesBefore)
	assert.Nil(t, params.Time)
	assert.Nil(t, params.ServiceDate)
	assert.Nil(t, params.StopSequence)
}
