package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestTripForVehicleHandlerEndToEnd(t *testing.T) {

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

func TestTripForVehicleHandlerWithServiceDate(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
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

func TestTripForVehicleHandlerWithTimeParameter(t *testing.T) {
	api, agencyID, vehicleID := setupTestApiWithMockVehicle(t)
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
}
