package restapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

func TestScheduleForStopHandler(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	defer api.Shutdown()

	// Get available agencies and stops for testing
	agencies := mustGetAgencies(t, api)
	assert.NotEmpty(t, agencies, "Test data should contain at least one agency")

	stops := mustGetStops(t, api)
	assert.NotEmpty(t, stops, "Test data should contain at least one stop")

	stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)

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
				data, ok := model.Data.(map[string]any)
				assert.True(t, ok)
				assert.NotNil(t, data["entry"])

				entry, ok := data["entry"].(map[string]any)
				assert.True(t, ok)
				assert.Equal(t, tt.stopID, entry["stopId"])

				loc, err := time.LoadLocation(agencies[0].Timezone)
				assert.NoError(t, err, "Should load agency timezone")

				parsedTime, err := time.ParseInLocation("2006-01-02", "2025-06-12", loc)
				assert.NoError(t, err, "Should parse test date")

				expectedMillis := float64(parsedTime.UnixMilli())
				assert.Equal(t, expectedMillis, entry["date"])

				assert.NotNil(t, entry["stopRouteSchedules"])
			}
		})
	}
}

func TestScheduleForStopHandlerDateParam(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Get valid stop for testing
	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)

	// Test valid date parameter
	t.Run("Valid date parameter in format YYYY-MM-DD", func(t *testing.T) {
		// NOTE: Hardcoded date 2025-06-12 used for test consistency with GTFS data validity
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]any)
		assert.True(t, ok)
		entry, ok := data["entry"].(map[string]any)
		assert.True(t, ok)
		assert.NotNil(t, entry["date"])
	})

	t.Run("Valid date parameter in format Unix Millisecond", func(t *testing.T) {
		loc, err := time.LoadLocation(agencies[0].Timezone)
		assert.NoError(t, err)

		// Input: June 12, 2025 12:00 PM local time
		inputTime := time.Date(2025, 6, 12, 12, 0, 0, 0, loc)
		inputMillis := inputTime.UnixMilli()

		endpoint := fmt.Sprintf("/api/where/schedule-for-stop/%s.json?key=TEST&date=%d", stopID, inputMillis)
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]any)
		assert.True(t, ok)
		entry, ok := data["entry"].(map[string]any)
		assert.True(t, ok)
		assert.NotNil(t, entry["date"])

		// Assert that the returned date echoes the EXACT input time
		assert.Equal(t, float64(inputMillis), entry["date"])
	})
}

func TestScheduleForStopHandlerAgencyTimeZone(t *testing.T) {
	clk := clock.NewMockClock(
		time.Date(2025, 12, 26, 23, 30, 0, 0, time.UTC),
	)
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)

	agency := agencies[0]
	stopID := utils.FormCombinedID(agency.ID, stops[0].ID)

	endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST"
	_, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

	data := model.Data.(map[string]any)
	entry, ok := data["entry"].(map[string]any)
	assert.True(t, ok)
	assert.NotNil(t, entry["date"])

	expected := clk.Now().UnixMilli()
	assert.Equal(t, float64(expected), entry["date"])
}

func TestScheduleForStopHandlerWithDateFiltering(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Get valid stop for testing
	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)

	tests := []struct {
		name           string
		date           string
		expectedStatus int
		validateResult func(t *testing.T, entry map[string]any)
	}{
		// NOTE: These dates (2025-06-12, etc.) are chosen to match the validity period of the
		// test GTFS data loaded in createTestApi. If the test data changes, these dates
		// must be updated to avoid test failures.
		{
			name:           "Thursday date - query executes successfully",
			date:           "2025-06-12",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, entry map[string]any) {
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
			validateResult: func(t *testing.T, entry map[string]any) {
				assert.Equal(t, stopID, entry["stopId"])
				_, exists := entry["stopRouteSchedules"]
				assert.True(t, exists, "stopRouteSchedules field should exist")
			},
		},
		{
			name:           "Sunday date - query executes successfully",
			date:           "2025-06-08",
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, entry map[string]any) {
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
				data, ok := model.Data.(map[string]any)
				assert.True(t, ok)
				entry, ok := data["entry"].(map[string]any)
				assert.True(t, ok)

				tt.validateResult(t, entry)
			}
		})
	}
}

func TestScheduleForStopHandlerReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)

	t.Run("Response structure is correct", func(t *testing.T) {
		// NOTE: Hardcoded date 2025-06-12 matches GTFS data validity
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]any)
		assert.True(t, ok, "Data should be a map")

		_, ok = data["references"].(map[string]any)
		assert.True(t, ok, "References should exist")

		entry, ok := data["entry"].(map[string]any)
		assert.True(t, ok, "Entry should exist")

		assert.Contains(t, entry, "stopId", "Entry should have stopId")
		assert.Contains(t, entry, "date", "Entry should have date")

		references := data["references"].(map[string]any)

		agenciesRef, ok := references["agencies"].([]any)
		assert.True(t, ok, "Agencies should exist")
		assert.True(t, len(agenciesRef) >= 1, "Should Have at least one Agency")

		stopsRef, ok := references["stops"].([]any)
		assert.True(t, ok, "Stops should exist in references")
		assert.Len(t, stopsRef, 1, "Should have exactly one stop")

		_, ok = references["routes"].([]any)
		assert.True(t, ok, "Routes should exist in references")
	})
}

func TestScheduleForStopHandlerInvalidDateFormat(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)

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
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)

	t.Run("Handler executes successfully", func(t *testing.T) {
		// NOTE: Hardcoded date matches GTFS data validity
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]any)
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]any)
		assert.True(t, ok)

		assert.Contains(t, entry, "stopId")
		assert.Contains(t, entry, "date")

	})
}

func TestScheduleForStopHandlerEmptyRoutes(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2025, 12, 26, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clk)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)

	t.Run("Stop with no routes returns empty schedule", func(t *testing.T) {
		stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)
		// NOTE: Hardcoded date matches GTFS data validity
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]any)
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]any)
		assert.True(t, ok)

		assert.NotNil(t, entry["stopRouteSchedules"])
	})
}

// TestScheduleForStopQueryValidation verifies the SQL query logic
func TestScheduleForStopQueryValidation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	require := assert.New(t)

	t.Run("Query returns valid data structure", func(t *testing.T) {
		stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2024-05-15"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		require.Equal(http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]any)
		require.True(ok, "Response data should be a map")

		// Validate references structure
		references, ok := data["references"].(map[string]any)
		require.True(ok, "References should exist and be a map")

		// Check that all reference types exist (even if empty)
		_, hasAgencies := references["agencies"]
		_, hasRoutes := references["routes"]

		require.True(hasAgencies || hasRoutes, "At least one reference type should exist")

		// trips must not contain full trip references for schedule-for-stop
		if rawTrips, hasTrips := references["trips"]; hasTrips {
			trips, ok := rawTrips.([]any)
			require.True(ok, "references.trips should be an array when present")
			require.Len(trips, 0, "references.trips must be empty for schedule-for-stop")
		}

		// Validate entry structure
		entry, ok := data["entry"].(map[string]any)
		require.True(ok, "Entry should exist")

		// Verify critical fields
		require.Equal(stopID, entry["stopId"], "StopID should match requested stop")
		require.NotNil(entry["date"], "Date should be set")

		// Verify stopRouteSchedules structure
		schedules, schedulesExists := entry["stopRouteSchedules"]
		require.True(schedulesExists, "stopRouteSchedules should exist")

		// If schedules exist, validate their structure
		if scheduleList, ok := schedules.([]any); ok && len(scheduleList) > 0 {
			firstSchedule := scheduleList[0].(map[string]any)

			// Verify route schedule has required fields
			require.Contains(firstSchedule, "routeId", "Route schedule should have routeId")
			require.Contains(firstSchedule, "stopRouteDirectionSchedules", "Route schedule should have stopRouteDirectionSchedules array")

			// Check direction schedules
			dirSchedules, ok := firstSchedule["stopRouteDirectionSchedules"].([]any)
			require.True(ok, "stopRouteDirectionSchedules should be an array")

			if len(dirSchedules) > 0 {
				dirSchedule := dirSchedules[0].(map[string]any)
				require.Contains(dirSchedule, "tripHeadsign", "Direction schedule should have tripHeadsign")
				require.Contains(dirSchedule, "scheduleStopTimes", "Direction schedule should have scheduleStopTimes")

				// Validate stop times
				stopTimes, ok := dirSchedule["scheduleStopTimes"].([]any)
				require.True(ok, "scheduleStopTimes should be an array")

				if len(stopTimes) > 0 {
					stopTime := stopTimes[0].(map[string]any)

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

					serviceID, ok := stopTime["serviceId"].(string)
					require.True(ok, "ServiceID should be a string")
					require.NotEmpty(serviceID, "ServiceID should not be empty")
					require.Contains(serviceID, "_", "serviceId should have agency prefix")

					// Verify that stop times are strictly sorted by departureTime
					for i := 0; i < len(stopTimes)-1; i++ {
						curr := stopTimes[i].(map[string]any)
						next := stopTimes[i+1].(map[string]any)
						currDep, okCurr := curr["departureTime"].(float64)
						nextDep, okNext := next["departureTime"].(float64)
						require.True(okCurr && okNext, "departureTime should be a number")
						require.LessOrEqual(currDep, nextDep, "Stop times must be sorted by departureTime ascending")
					}
				}
			}
		}
	})

	t.Run("Query handles different weekdays correctly", func(t *testing.T) {
		// Create a fresh API instance to avoid rate limiting
		testApi := createTestApi(t)
		testAgencies := mustGetAgencies(t, testApi)
		testStops := mustGetStops(t, testApi)
		testStopID := utils.FormCombinedID(testAgencies[0].ID, testStops[0].ID)

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

				data, ok := model.Data.(map[string]any)
				assert.True(t, ok, "Data should be a map for %s", tt.weekday)

				entry, ok := data["entry"].(map[string]any)
				assert.True(t, ok, "Entry should exist for %s", tt.weekday)

				_, exists := entry["stopRouteSchedules"]
				assert.True(t, exists, "stopRouteSchedules should exist for %s", tt.weekday)
			})
		}
	})

	t.Run("Query properly formats timestamps", func(t *testing.T) {
		stopID := utils.FormCombinedID(agencies[0].ID, stops[0].ID)
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2024-05-15"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		require.Equal(http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]any)
		require.True(ok)

		entry, ok := data["entry"].(map[string]any)
		require.True(ok)

		// Verify date is a Unix timestamp in milliseconds
		date, ok := entry["date"].(float64)
		require.True(ok, "Date should be a number")
		require.Greater(date, float64(0), "Date should be positive")

		// Check if we have schedules with stop times
		if schedules, ok := entry["stopRouteSchedules"].([]any); ok && len(schedules) > 0 {
			firstSchedule := schedules[0].(map[string]any)
			if dirSchedules, ok := firstSchedule["stopRouteDirectionSchedules"].([]any); ok && len(dirSchedules) > 0 {
				dirSchedule := dirSchedules[0].(map[string]any)
				if stopTimes, ok := dirSchedule["scheduleStopTimes"].([]any); ok && len(stopTimes) > 0 {
					stopTime := stopTimes[0].(map[string]any)

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

func TestScheduleForStopHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/schedule-for-stop/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestScheduleForStopHandlerBlockSequenceLogic(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	require := assert.New(t)

	// Helper function to fetch stop times for a specific stop
	fetchStopTimesForStop := func(stopIDStr string) map[string]map[string]any {
		stopID := utils.FormCombinedID(agencies[0].ID, stopIDStr)
		// NOTE: Hardcoded date matches the mock GTFS data validity
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)
		require.Equal(http.StatusOK, resp.StatusCode)

		data := model.Data.(map[string]any)
		entry := data["entry"].(map[string]any)
		schedules := entry["stopRouteSchedules"].([]any)

		// Flatten all stop times into a map for easy lookup by Trip ID
		stopTimesByTrip := make(map[string]map[string]any)
		for _, schedAny := range schedules {
			sched := schedAny.(map[string]any)
			for _, dirSchedAny := range sched["stopRouteDirectionSchedules"].([]any) {
				dirSched := dirSchedAny.(map[string]any)
				for _, stAny := range dirSched["scheduleStopTimes"].([]any) {
					st := stAny.(map[string]any)
					tripID := st["tripId"].(string)
					stopTimesByTrip[tripID] = st
				}
			}
		}
		return stopTimesByTrip
	}

	t.Run("Evaluates absolute first stop as false", func(t *testing.T) {
		stopTimesByTrip := fetchStopTimesForStop("1030")
		firstTripID := utils.FormCombinedID(agencies[0].ID, "84f4520e-88b6-4ee6-8975-856799bc1359")
		st, ok := stopTimesByTrip[firstTripID]
		require.True(ok, "trip not found in response for first stop")
		assert.False(t, st["arrivalEnabled"].(bool), "arrivalEnabled must be FALSE for the absolute first stop")
		assert.True(t, st["departureEnabled"].(bool), "departureEnabled must be TRUE")
	})

	t.Run("Evaluates middle stop as true", func(t *testing.T) {
		stopTimesByTrip := fetchStopTimesForStop("1014")
		middleTripID := utils.FormCombinedID(agencies[0].ID, "109522ca-5218-47f9-9cd0-123648acfe17")
		st, ok := stopTimesByTrip[middleTripID]
		require.True(ok, "trip not found in response for middle stop")
		assert.True(t, st["arrivalEnabled"].(bool), "arrivalEnabled must be TRUE for a middle stop")
		assert.True(t, st["departureEnabled"].(bool), "departureEnabled must be TRUE for a middle stop")
	})

	t.Run("Evaluates absolute last stop as false", func(t *testing.T) {
		stopTimesByTrip := fetchStopTimesForStop("2000")
		lastTripID := utils.FormCombinedID(agencies[0].ID, "b137c8a8-db88-4f7b-8b7f-4ccfe1ee4103")
		st, ok := stopTimesByTrip[lastTripID]
		require.True(ok, "trip not found in response for last stop")
		assert.True(t, st["arrivalEnabled"].(bool), "arrivalEnabled must be TRUE")
		assert.False(t, st["departureEnabled"].(bool), "departureEnabled must be FALSE for the absolute last stop")
	})
}

func TestScheduleForStopHandlerDirectionPartitioning(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencies := mustGetAgencies(t, api)
	stops := mustGetStops(t, api)
	require := assert.New(t)

	t.Run("Validates direction partitioning and alphabetical sorting by headsign", func(t *testing.T) {
		// Iterate through all stops on active test date 2025-06-12
		for _, stop := range stops {
			stopID := utils.FormCombinedID(agencies[0].ID, stop.ID)
			endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=org.onebusaway.iphone&date=2025-06-12"
			resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)
			require.Equal(http.StatusOK, resp.StatusCode)

			data, ok := model.Data.(map[string]any)
			require.True(ok)
			entry, ok := data["entry"].(map[string]any)
			require.True(ok)

			schedules, ok := entry["stopRouteSchedules"].([]any)
			require.True(ok)

			// Route schedules must be sorted alphabetically by routeId
			for i := 0; i < len(schedules)-1; i++ {
				currSched := schedules[i].(map[string]any)
				nextSched := schedules[i+1].(map[string]any)
				currRouteID, _ := currSched["routeId"].(string)
				nextRouteID, _ := nextSched["routeId"].(string)
				assert.LessOrEqual(t, currRouteID, nextRouteID, "Route schedules must be sorted alphabetically by routeId")
			}

			for _, schedAny := range schedules {
				sched := schedAny.(map[string]any)
				dirSchedules, ok := sched["stopRouteDirectionSchedules"].([]any)
				require.True(ok, "stopRouteDirectionSchedules should be an array")

				// Direction groups within each route are sorted alphabetically by headsign
				for i := 0; i < len(dirSchedules)-1; i++ {
					currDir := dirSchedules[i].(map[string]any)
					nextDir := dirSchedules[i+1].(map[string]any)
					currHeadsign, _ := currDir["tripHeadsign"].(string)
					nextHeadsign, _ := nextDir["tripHeadsign"].(string)
					assert.LessOrEqual(t, currHeadsign, nextHeadsign, "Direction schedules must be sorted alphabetically by tripHeadsign")
				}
			}
		}
	})
}
