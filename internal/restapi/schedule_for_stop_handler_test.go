package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/utils"
)

func TestScheduleForStopHandler(t *testing.T) {
	api := createTestApi(t)

	// Get available agencies and stops for testing
	agencies := api.GtfsManager.GetAgencies()
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	stops := api.GtfsManager.GetStops()
	assert.NotEmpty(t, stops, "Test data should contain at least one stop")

	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	tests := []struct {
		name                string
		stopID              string
		expectedStatus      int
		expectValidResponse bool
	}{
		{
			name:                "Valid stop",
			stopID:              stopID,
			expectedStatus:      http.StatusOK,
			expectValidResponse: true,
		},
		{
			name:                "Invalid stop ID",
			stopID:              "nonexistent_stop",
			expectedStatus:      http.StatusNotFound,
			expectValidResponse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/schedule-for-stop/"+tt.stopID+".json?key=TEST")

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Equal(t, tt.expectedStatus, model.Code)

			if tt.expectValidResponse {
				assert.Equal(t, "OK", model.Text)
				data, ok := model.Data.(map[string]interface{})
				assert.True(t, ok)
				assert.NotNil(t, data["entry"])

				entry, ok := data["entry"].(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, tt.stopID, entry["stopId"])
				assert.NotNil(t, entry["date"])
				assert.NotNil(t, entry["stopRouteSchedules"])
			}
		})
	}
}

func TestScheduleForStopHandlerDateParam(t *testing.T) {
	api := createTestApi(t)

	// Get valid stop for testing
	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	// Test valid date parameter
	t.Run("Valid date parameter", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)
		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)
		assert.NotNil(t, entry["date"])
	})
}

func TestScheduleForStopHandlerWithDateFiltering(t *testing.T) {
	api := createTestApi(t)

	// Get valid stop for testing
	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	tests := []struct {
		name           string
		date           string
		expectedStatus int
		validateResult func(t *testing.T, entry map[string]interface{})
	}{
		{
			name:           "Thursday date - query executes successfully",
			date:           "2025-06-12",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, entry map[string]interface{}) {
				assert.Equal(t, stopID, entry["stopId"])
				assert.NotNil(t, entry["date"])
				_, exists := entry["stopRouteSchedules"]
				assert.True(t, exists, "stopRouteSchedules field should exist")
			},
		},
		{
			name:           "Monday date - query executes successfully",
			date:           "2025-06-09",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, entry map[string]interface{}) {
				assert.Equal(t, stopID, entry["stopId"])
				_, exists := entry["stopRouteSchedules"]
				assert.True(t, exists, "stopRouteSchedules field should exist")
			},
		},
		{
			name:           "Sunday date - query executes successfully",
			date:           "2025-06-08",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, entry map[string]interface{}) {
				assert.Equal(t, stopID, entry["stopId"])
				_, exists := entry["stopRouteSchedules"]
				assert.True(t, exists, "stopRouteSchedules field should exist")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=" + tt.date
			resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Equal(t, tt.expectedStatus, model.Code)

			if tt.expectedStatus == http.StatusOK {
				data, ok := model.Data.(map[string]interface{})
				assert.True(t, ok)
				entry, ok := data["entry"].(map[string]interface{})
				assert.True(t, ok)

				tt.validateResult(t, entry)
			}
		})
	}
}

func TestScheduleForStopHandlerTripReferences(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	t.Run("Response structure is correct", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok, "Data should be a map")

		_, ok = data["references"].(map[string]interface{})
		assert.True(t, ok, "References should exist")

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok, "Entry should exist")

		assert.Contains(t, entry, "stopId", "Entry should have stopId")
		assert.Contains(t, entry, "date", "Entry should have date")

	})
}

func TestScheduleForStopHandlerInvalidDateFormat(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	tests := []struct {
		name           string
		date           string
		expectedStatus int
	}{
		{
			name:           "Invalid date format - wrong separator",
			date:           "2025/06/12",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid date format - incomplete",
			date:           "2025-06",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid date - not a real date",
			date:           "2025-13-45",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=" + tt.date
			resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			if model.Code != 0 {
				assert.Equal(t, tt.expectedStatus, model.Code)
			}
		})
	}
}

func TestScheduleForStopHandlerScheduleContent(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	t.Run("Handler executes successfully", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		assert.Contains(t, entry, "stopId")
		assert.Contains(t, entry, "date")

	})
}

func TestScheduleForStopHandlerEmptyRoutes(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()

	t.Run("Stop with no routes returns empty schedule", func(t *testing.T) {
		stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		assert.NotNil(t, entry["stopRouteSchedules"])
	})
}
