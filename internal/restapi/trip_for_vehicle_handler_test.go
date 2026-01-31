package restapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// Helper to create a mock vehicle and inject it into the test API
func setupTestApiWithMockVehicle(t *testing.T) (*RestAPI, string, string) {
	api := createTestApi(t)
	// Initialize the logger to prevent nil pointer panics during handler execution
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Note: caller is responsible for calling api.Shutdown()

	agencyStatic := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := trips[0].ID
	agencyID := agencyStatic.Id
	vehicleID := "MOCK_VEHICLE_1"
	routeID := utils.FormCombinedID(agencyID, trips[0].Route.Id)

	api.GtfsManager.MockAddAgency(agencyID, "unitrans")
	api.GtfsManager.MockAddRoute(routeID, agencyID, routeID)
	api.GtfsManager.MockAddTrip(tripID, agencyID, routeID)
	api.GtfsManager.MockAddVehicle(vehicleID, tripID, routeID)

	return api, agencyID, vehicleID
}

func TestTripForVehicleHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trip-for-vehicle/invalid.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestTripForVehicleHandlerContentTypeHeader(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	contentType := resp.Header.Get("Content-Type")
	assert.Equal(t, "application/json", contentType, "Content-Type should be application/json")
}

func TestTripForVehicleHandlerResponseSchemaValidation(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	// Validate top-level response structure
	assert.Equal(t, http.StatusOK, model.Code, "Response code should be 200")
	assert.Equal(t, "OK", model.Text, "Response text should be 'OK'")
	assert.Equal(t, 2, model.Version, "Response version should be 2")
	assert.Greater(t, model.CurrentTime, int64(0), "CurrentTime should be set")

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	// Validate entry structure
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "Entry should exist")

	// Required fields in entry
	assert.Contains(t, entry, "tripId", "Entry should contain tripId")
	assert.Contains(t, entry, "serviceDate", "Entry should contain serviceDate")

	// Validate serviceDate is a positive number
	serviceDate, ok := entry["serviceDate"].(float64)
	assert.True(t, ok, "serviceDate should be a number")
	assert.Greater(t, serviceDate, float64(0), "serviceDate should be positive")

	// Validate references structure
	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "References should exist")

	// Check required reference arrays exist
	assert.Contains(t, references, "agencies", "References should contain agencies")
	assert.Contains(t, references, "routes", "References should contain routes")
	assert.Contains(t, references, "stops", "References should contain stops")
	assert.Contains(t, references, "trips", "References should contain trips")
	// situations might be optional depending on implementation, but good to check if expected
}

func TestTripForVehicleHandlerEndToEnd(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()

	ctx := context.Background()
	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	require.NoError(t, err)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	assert.NotNil(t, entry["tripId"])
	assert.NotNil(t, entry["serviceDate"])

	loc, err := time.LoadLocation(agency.Timezone)
	if err != nil {
		loc = time.UTC
	}

	currentTimeInLoc := time.Now().In(loc)
	y, m, d := currentTimeInLoc.Date()
	expectedServiceDate := time.Date(y, m, d, 0, 0, 0, 0, loc)
	expectedServiceDateMillis := expectedServiceDate.Unix() * 1000
	assert.Equal(t, float64(expectedServiceDateMillis), entry["serviceDate"])

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

	routes, ok := references["routes"].([]interface{})
	assert.True(t, ok, "Routes section should exist in references")
	assert.NotEmpty(t, routes, "Routes should not be empty")

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "Agencies section should exist in references")
	assert.NotEmpty(t, agencies, "Agencies should not be empty")

	// Ensure trip is included by default
	trips, ok := references["trips"].([]interface{})
	assert.True(t, ok, "Trips section should exist in references by default")
	assert.NotEmpty(t, trips, "Trips should not be empty by default")

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

func TestTripForVehicleHandlerWithInvalidVehicleID(t *testing.T) {
	api, agencyID, _ := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()
	vehicleCombinedID := utils.FormCombinedID(agencyID, "invalid")

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

// Check for edge case: Vehicle exists but has no current trip (Idle)
func TestTripForVehicleHandlerWithIdleVehicle(t *testing.T) {
	api, agencyID, _ := setupTestApiWithMockVehicle(t)

	// Create a vehicle with empty trip ID (tests vehicle.Trip.ID.ID == "" branch)
	idleVehicleID := "IDLE_VEHICLE"
	api.GtfsManager.MockAddVehicle(idleVehicleID, "", "")

	vehicleCombinedID := utils.FormCombinedID(agencyID, idleVehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	// Should return 404 Not Found as the vehicle has no trip
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
}

// Ensure proper handling when a vehicle references a trip that does not exist in the DB (sql.ErrNoRows)
func TestTripForVehicleHandlerWithNonExistentTrip(t *testing.T) {
	api := createTestApi(t)
	// Initialize the logger to prevent nil pointer panics during handler execution
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	defer api.Shutdown()

	agencyID := api.GtfsManager.GetAgencies()[0].Id

	// Create vehicle with trip ID that doesn't exist in DB
	vehicleID := "GHOST_TRIP_VEHICLE"
	nonExistentTripID := "TRIP_THAT_DOES_NOT_EXIST"
	api.GtfsManager.MockAddVehicle(vehicleID, nonExistentTripID, "some_route")

	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestTripForVehicleHandlerWithInvalidAgencyID(t *testing.T) {
	api, _, vehicleID := setupTestApiWithMockVehicle(t)
	// Use a non-existent agency ID
	invalidAgencyVehicleID := utils.FormCombinedID("INVALID_AGENCY", vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + invalidAgencyVehicleID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Should return 404 (Not Found) because GetAgency returns sql.ErrNoRows, which is handled as 404
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestTripForVehicleHandlerWithServiceDate(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)
	tomorrow := time.Now().AddDate(0, 0, 1)
	serviceDateMs := tomorrow.Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&serviceDate=" + strconv.FormatInt(serviceDateMs, 10))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, float64(serviceDateMs), entry["serviceDate"])
}

func TestTripForVehicleHandlerWithIncludeStatusFalse(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&includeStatus=false")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	status, statusExists := entry["status"]
	if statusExists {
		assert.Nil(t, status, "Status should be nil when includeStatus=false")
	}
}

func TestTripForVehicleHandlerWithIncludeTripFalse(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Explicitly set includeTrip=false
	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&includeTrip=false")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	assert.True(t, ok)

	// Check that trips are NOT in references
	trips, tripsExists := references["trips"]
	if tripsExists {
		// If the key exists, it should be nil or empty list
		tripsList, isList := trips.([]interface{})
		if isList {
			assert.Empty(t, tripsList, "Trips should be empty when includeTrip=false")
		} else {
			assert.Nil(t, trips)
		}
	}
}

func TestTripForVehicleHandlerWithIncludeScheduleTrue(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&includeSchedule=true")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	// When includeSchedule=true, schedule may be present (depends on data)
	// Just ensure the request succeeds and basic data is there
	assert.NotNil(t, entry["tripId"])
}

func TestTripForVehicleHandlerWithTimeParameter(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)
	specificTime := time.Now().Add(1 * time.Hour)
	timeMs := specificTime.Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&time=" + strconv.FormatInt(timeMs, 10))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)
	assert.NotNil(t, entry["tripId"])
}

func TestTripForVehicleHandlerWithAllParametersFalse(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&includeTrip=false&includeSchedule=false&includeStatus=false")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	// Basic fields should still exist
	assert.NotNil(t, entry["tripId"])
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

	agencies, ok := references["agencies"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, agencies)

	// Ensure trip is missing from references
	trips, tripsExists := references["trips"]
	if tripsExists {
		tripsList, ok := trips.([]interface{})
		if ok {
			assert.Empty(t, tripsList)
		}
	}
}

func TestTripForVehicleHandlerWithCombinedParameters(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	serviceDate := time.Now().Truncate(24 * time.Hour)
	serviceDateMs := serviceDate.Unix() * 1000
	timeMs := time.Now().Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	url := server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID +
		".json?key=TEST&includeTrip=true&includeSchedule=true&includeStatus=true" +
		"&serviceDate=" + strconv.FormatInt(serviceDateMs, 10) +
		"&time=" + strconv.FormatInt(timeMs, 10)

	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	assert.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	assert.True(t, ok)

	assert.Equal(t, float64(serviceDateMs), entry["serviceDate"])
}

func TestTripForVehicleHandlerAgencyReferenceValidation(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := references["agencies"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, agencies, "At least one agency should be referenced")

	// Validate agency structure
	agency, ok := agencies[0].(map[string]interface{})
	require.True(t, ok)

	assert.Contains(t, agency, "id", "Agency should have id")
	assert.Contains(t, agency, "name", "Agency should have name")
	assert.Contains(t, agency, "url", "Agency should have url")
	assert.Contains(t, agency, "timezone", "Agency should have timezone")
}

func TestTripForVehicleHandlerTripReferenceValidation(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	trips, ok := references["trips"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, trips, "At least one trip should be referenced")

	// Validate trip structure
	trip, ok := trips[0].(map[string]interface{})
	require.True(t, ok)

	assert.Contains(t, trip, "id", "Trip should have id")
	assert.Contains(t, trip, "routeId", "Trip should have routeId")
	assert.Contains(t, trip, "serviceId", "Trip should have serviceId")
}

func TestTripForVehicleHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/trip-for-vehicle/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestTripForVehicleHandlerWithInvalidParams(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()
	vehicleCombinedID := utils.FormCombinedID(agencyID, vehicleID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST&serviceDate=invalid")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp2, err := http.Get(server.URL + "/api/where/trip-for-vehicle/" + vehicleCombinedID + ".json?key=TEST&time=invalid")
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

func TestParseTripForVehicleParams_Unit(t *testing.T) {
	api, _, _ := setupTestApiWithMockVehicle(t)
	defer api.Shutdown()

	req := httptest.NewRequest("GET", "/?includeStatus=false&time=1609459200000", nil)
	params, errs := api.parseTripForVehicleParams(req)

	assert.Nil(t, errs)
	assert.False(t, params.IncludeStatus)
	assert.NotNil(t, params.Time)

	reqDefault := httptest.NewRequest("GET", "/", nil)
	paramsDefault, errsDefault := api.parseTripForVehicleParams(reqDefault)

	assert.Nil(t, errsDefault)
	assert.True(t, paramsDefault.IncludeTrip)
	assert.False(t, paramsDefault.IncludeSchedule)
	assert.True(t, paramsDefault.IncludeStatus)

	reqInvalid := httptest.NewRequest("GET", "/?serviceDate=invalid&time=invalid", nil)
	_, errsInvalid := api.parseTripForVehicleParams(reqInvalid)

	assert.NotNil(t, errsInvalid)
	assert.Contains(t, errsInvalid, "serviceDate")
	assert.Contains(t, errsInvalid, "time")
	assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errsInvalid["serviceDate"][0])
}
