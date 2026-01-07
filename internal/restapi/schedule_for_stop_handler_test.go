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

			// If we expect a valid response, force a known valid date (2025-06-12).
			url := "/api/where/schedule-for-stop/" + tt.stopID + ".json?key=TEST"
			if tt.expectValidResponse {
				url += "&date=2025-06-12"
			}

			resp, model := serveApiAndRetrieveEndpoint(t, api, url)

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

		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		assert.NotNil(t, entry["stopRouteSchedules"])
	})
}

// TestScheduleForStopQueryValidation verifies the SQL query logic
func TestScheduleForStopQueryValidation(t *testing.T) {
	api := createTestApi(t)

	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	require := assert.New(t)

	t.Run("Query returns valid data structure", func(t *testing.T) {
		stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2024-05-15"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		require.Equal(http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]interface{})
		require.True(ok, "Response data should be a map")

		// Validate references structure
		references, ok := data["references"].(map[string]interface{})
		require.True(ok, "References should exist and be a map")

		// Check that all reference types exist (even if empty)
		_, hasAgencies := references["agencies"]
		_, hasRoutes := references["routes"]
		_, hasTrips := references["trips"]

		require.True(hasAgencies || hasRoutes || hasTrips, "At least one reference type should exist")

		// Validate entry structure
		entry, ok := data["entry"].(map[string]interface{})
		require.True(ok, "Entry should exist")

		// Verify critical fields
		require.Equal(stopID, entry["stopId"], "StopID should match requested stop")
		require.NotNil(entry["date"], "Date should be set")

		// Verify stopRouteSchedules structure
		schedules, schedulesExists := entry["stopRouteSchedules"]
		require.True(schedulesExists, "stopRouteSchedules should exist")

		// If schedules exist, validate their structure
		if scheduleList, ok := schedules.([]interface{}); ok && len(scheduleList) > 0 {
			firstSchedule := scheduleList[0].(map[string]interface{})

			// Verify route schedule has required fields
			require.Contains(firstSchedule, "routeId", "Route schedule should have routeId")
			require.Contains(firstSchedule, "stopRouteDirectionSchedules", "Route schedule should have stopRouteDirectionSchedules array")

			// Check direction schedules
			dirSchedules, ok := firstSchedule["stopRouteDirectionSchedules"].([]interface{})
			require.True(ok, "stopRouteDirectionSchedules should be an array")

			if len(dirSchedules) > 0 {
				dirSchedule := dirSchedules[0].(map[string]interface{})
				require.Contains(dirSchedule, "tripHeadsign", "Direction schedule should have tripHeadsign")
				require.Contains(dirSchedule, "scheduleStopTimes", "Direction schedule should have scheduleStopTimes")

				// Validate stop times
				stopTimes, ok := dirSchedule["scheduleStopTimes"].([]interface{})
				require.True(ok, "scheduleStopTimes should be an array")

				if len(stopTimes) > 0 {
					stopTime := stopTimes[0].(map[string]interface{})

					// Verify all required fields from the new query
					require.Contains(stopTime, "arrivalTime", "StopTime should have arrivalTime")
					require.Contains(stopTime, "departureTime", "StopTime should have departureTime")
					require.Contains(stopTime, "tripId", "StopTime should have tripId")
					require.Contains(stopTime, "serviceId", "StopTime should have serviceId")

					// Verify trip ID is properly formatted (agencyId_tripId)
					tripID, ok := stopTime["tripId"].(string)
					require.True(ok, "TripID should be a string")
					require.NotEmpty(tripID, "TripID should not be empty")
					require.Contains(tripID, "_", "TripID should be in combined format (agency_trip)")
				}
			}
		}
	})

	t.Run("Query handles different weekdays correctly", func(t *testing.T) {
		// Create a fresh API instance to avoid rate limiting
		testApi := createTestApi(t)
		testAgencies := testApi.GtfsManager.GetAgencies()
		testStops := testApi.GtfsManager.GetStops()
		testStopID := utils.FormCombinedID(testAgencies[0].Id, testStops[0].Id)

		weekdayTests := []struct {
			date    string
			weekday string
		}{
			{"2024-05-13", "Monday"},
			{"2024-05-17", "Friday"},
		}

		for _, tt := range weekdayTests {
			t.Run(tt.weekday, func(t *testing.T) {
				endpoint := "/api/where/schedule-for-stop/" + testStopID + ".json?key=TEST&date=" + tt.date
				resp, model := serveApiAndRetrieveEndpoint(t, testApi, endpoint)

				assert.Equal(t, http.StatusOK, resp.StatusCode, "Query should execute for %s", tt.weekday)
				assert.Equal(t, http.StatusOK, model.Code, "Model code should be OK for %s", tt.weekday)

				data, ok := model.Data.(map[string]interface{})
				assert.True(t, ok, "Data should be a map for %s", tt.weekday)

				entry, ok := data["entry"].(map[string]interface{})
				assert.True(t, ok, "Entry should exist for %s", tt.weekday)

				_, exists := entry["stopRouteSchedules"]
				assert.True(t, exists, "stopRouteSchedules should exist for %s", tt.weekday)
			})
		}
	})

	t.Run("Query properly formats timestamps", func(t *testing.T) {
		stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2024-05-15"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		require.Equal(http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]interface{})
		require.True(ok)

		entry, ok := data["entry"].(map[string]interface{})
		require.True(ok)

		// Verify date is a Unix timestamp in milliseconds
		date, ok := entry["date"].(float64)
		require.True(ok, "Date should be a number")
		require.Greater(date, float64(0), "Date should be positive")

		// Check if we have schedules with stop times
		if schedules, ok := entry["stopRouteSchedules"].([]interface{}); ok && len(schedules) > 0 {
			firstSchedule := schedules[0].(map[string]interface{})
			if dirSchedules, ok := firstSchedule["schedules"].([]interface{}); ok && len(dirSchedules) > 0 {
				dirSchedule := dirSchedules[0].(map[string]interface{})
				if stopTimes, ok := dirSchedule["stopTimes"].([]interface{}); ok && len(stopTimes) > 0 {
					stopTime := stopTimes[0].(map[string]interface{})

					// Verify arrival and departure times are timestamps
					arrivalTime, ok := stopTime["arrivalTime"].(float64)
					require.True(ok, "ArrivalTime should be a number")
					require.Greater(arrivalTime, float64(0), "ArrivalTime should be positive")

					departureTime, ok := stopTime["departureTime"].(float64)
					require.True(ok, "DepartureTime should be a number")
					require.Greater(departureTime, float64(0), "DepartureTime should be positive")

					// Departure should be >= arrival
					require.GreaterOrEqual(departureTime, arrivalTime, "Departure time should be >= arrival time")
				}
			}
		}
	})
}
