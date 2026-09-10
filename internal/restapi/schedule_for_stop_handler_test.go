package restapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
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

	t.Run("Ignore references when includeReferences=false", func(t *testing.T) {
		endpoint := "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12&includeReferences=false"
		resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data, ok := model.Data.(map[string]any)
		assert.True(t, ok, "Data should be a map")

		references, ok := data["references"].(map[string]any)
		assert.True(t, ok, "References should exist")

		agenciesRef, ok := references["agencies"].([]any)
		assert.True(t, ok, "Agencies should be an array")
		assert.Len(t, agenciesRef, 0, "Agencies array should be empty")

		stopsRef, ok := references["stops"].([]any)
		assert.True(t, ok, "Stops should be an array")
		assert.Len(t, stopsRef, 0, "Stops array should be empty")

		routesRef, ok := references["routes"].([]any)
		assert.True(t, ok, "Routes should be an array")
		assert.Len(t, routesRef, 0, "Routes array should be empty")
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

// referenceIDs pulls the id out of each record in a references list, for comparing a
// response's references against the set the fixture says it should contain.
func referenceIDs(t testing.TB, referenceList any) []string {
	t.Helper()

	records, ok := referenceList.([]any)
	require.True(t, ok, "reference list should be an array")

	ids := make([]string, 0, len(records))
	for _, record := range records {
		id, ok := record.(map[string]any)["id"].(string)
		require.True(t, ok, "every reference record should carry a string id")
		ids = append(ids, id)
	}

	return ids
}

func TestScheduleForStopHandlerReferencesWithoutSchedule(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyID := mustGetAgencies(t, api)[0].ID
	routelessStopID := utils.FormCombinedID(agencyID, mustGetStopWithoutRoutes(t, api))

	// The served stop's own routes are the reference set the handler is expected to
	// return, whatever the queried date, so derive both expectations from them.
	servedStop := mustGetStop(t, api)
	servingRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(context.Background(), servedStop.ID)
	require.NoError(t, err)
	require.NotEmpty(t, servingRoutes, "the served stop should have routes to reference")

	wantRouteIDs := make([]string, 0, len(servingRoutes))
	wantAgencyIDs := make([]string, 0, len(servingRoutes))
	for _, route := range servingRoutes {
		wantRouteIDs = append(wantRouteIDs, utils.FormCombinedID(agencyID, route.ID))
		if !slices.Contains(wantAgencyIDs, route.AgencyID) {
			wantAgencyIDs = append(wantAgencyIDs, route.AgencyID)
		}
	}

	tests := []struct {
		name          string
		endpoint      string
		wantStopRef   bool
		wantRouteIDs  []string
		wantAgencyIDs []string
	}{
		{
			// The stop still has to be resolvable from entry.stopId even though
			// it has nothing scheduled.
			name:        "stop with no routes still references the stop",
			endpoint:    "/api/where/schedule-for-stop/" + routelessStopID + ".json?key=org.onebusaway.iphone",
			wantStopRef: true,
		},
		{
			name:     "stop with no routes honors includeReferences=false",
			endpoint: "/api/where/schedule-for-stop/" + routelessStopID + ".json?key=org.onebusaway.iphone&includeReferences=false",
		},
		{
			// NOTE: Hardcoded date falls outside the GTFS data's validity period.
			name:          "routes stay referenced on a date with no service",
			endpoint:      "/api/where/schedule-for-stop/" + utils.FormCombinedID(agencyID, servedStop.ID) + ".json?key=org.onebusaway.iphone&date=2030-01-01",
			wantStopRef:   true,
			wantRouteIDs:  wantRouteIDs,
			wantAgencyIDs: wantAgencyIDs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data, ok := model.Data.(map[string]any)
			require.True(t, ok)

			entry, ok := data["entry"].(map[string]any)
			require.True(t, ok)
			assert.Empty(t, entry["stopRouteSchedules"])

			references, ok := data["references"].(map[string]any)
			require.True(t, ok)

			stops, _ := references["stops"].([]any)
			if tt.wantStopRef {
				require.Len(t, stops, 1)
				stopRef, ok := stops[0].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, entry["stopId"], stopRef["id"])
			} else {
				assert.Empty(t, stops)
			}

			assert.ElementsMatch(t, tt.wantRouteIDs, referenceIDs(t, references["routes"]))
			assert.ElementsMatch(t, tt.wantAgencyIDs, referenceIDs(t, references["agencies"]))
		})
	}
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

			references, ok := data["references"].(map[string]any)
			require.True(ok)
			routeRefs, ok := references["routes"].([]any)
			require.True(ok)

			// Build routeId -> sort name (shortName, falling back to longName) from references,
			// matching the natural-sort key the spec requires for stopRouteSchedules ordering.
			routeSortNameByID := make(map[string]string, len(routeRefs))
			for _, routeAny := range routeRefs {
				route := routeAny.(map[string]any)
				id, _ := route["id"].(string)
				shortName, _ := route["shortName"].(string)
				longName, _ := route["longName"].(string)
				name := shortName
				if name == "" {
					name = longName
				}
				routeSortNameByID[id] = name
			}

			// Route schedules must be sorted by route short name (falling back to long name)
			// using a natural (numeric-aware) string sort, not a plain lexicographic sort on routeId.
			for i := 0; i < len(schedules)-1; i++ {
				currSched := schedules[i].(map[string]any)
				nextSched := schedules[i+1].(map[string]any)
				currRouteID, _ := currSched["routeId"].(string)
				nextRouteID, _ := nextSched["routeId"].(string)
				currName := routeSortNameByID[currRouteID]
				nextName := routeSortNameByID[nextRouteID]
				assert.LessOrEqual(t, utils.NaturalCompare(currName, nextName), 0,
					"Route schedules must be sorted naturally by route short/long name (%q vs %q)", currName, nextName)
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

func TestGroupScheduleRowsByRouteAndDirection(t *testing.T) {
	startOfDay := time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)
	agencyID := "1"
	rowCtx := scheduleRowContext{agencyID: agencyID, startOfDay: startOfDay}

	makeRow := func(tripID, routeID string, directionID sql.NullInt64, headsign string) gtfsdb.GetScheduleForStopOnDateRow {
		return gtfsdb.GetScheduleForStopOnDateRow{
			TripID:        tripID,
			ArrivalTime:   int64(8 * time.Hour),
			DepartureTime: int64(8 * time.Hour),
			ServiceID:     "svc1",
			RouteID:       routeID,
			AgencyID:      agencyID,
			DirectionID:   directionID,
			TripHeadsign:  sql.NullString{String: headsign, Valid: headsign != ""},
		}
	}

	t.Run("splits rows on the same route into separate direction groups", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-out", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-in", "10", sql.NullInt64{Int64: 1, Valid: true}, "Uptown"),
		}

		schedules, _, _, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		routeGroups, ok := schedules["1_10"]
		assert.True(t, ok, "expected a group for route 1_10")
		assert.Len(t, routeGroups, 2, "expected two distinct direction buckets")

		assert.Len(t, routeGroups["0"], 1)
		assert.Equal(t, "1_trip-out", routeGroups["0"][0].TripID)

		assert.Len(t, routeGroups["1"], 1)
		assert.Equal(t, "1_trip-in", routeGroups["1"][0].TripID)
	})

	t.Run("groups rows on the same route and direction together", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-a", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-b", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
		}

		schedules, _, headsignCounts, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		assert.Len(t, schedules["1_10"], 1, "expected a single direction bucket")
		assert.Len(t, schedules["1_10"]["0"], 2, "expected both stop times grouped together")
		assert.Equal(t, 2, headsignCounts["1_10"]["0"]["Downtown"])
	})

	t.Run("defaults a missing direction_id to bucket 0", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-a", "10", sql.NullInt64{Valid: false}, "Downtown"),
		}

		schedules, _, _, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		assert.Len(t, schedules["1_10"], 1)
		assert.Contains(t, schedules["1_10"], "0")
		assert.Len(t, schedules["1_10"]["0"], 1)
	})

	t.Run("tracks headsign votes separately per direction", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-out-1", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-out-2", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-in-1", "10", sql.NullInt64{Int64: 1, Valid: true}, "Uptown"),
		}

		_, _, headsignCounts, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		assert.Equal(t, 2, headsignCounts["1_10"]["0"]["Downtown"])
		assert.Equal(t, 1, headsignCounts["1_10"]["1"]["Uptown"])
		assert.Equal(t, 0, headsignCounts["1_10"]["1"]["Downtown"], "direction 1 should not see direction 0's headsign votes")
	})

	t.Run("returns an error when the context is already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-a", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
		}

		_, _, _, err := groupScheduleRowsByRouteAndDirection(ctx, rows, rowCtx)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("routes frequency-based trips to the frequency map", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-freq", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-normal", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
		}
		rowCtxWithFreqs := rowCtx
		rowCtxWithFreqs.freqMap = map[string][]gtfsdb.Frequency{
			"trip-freq": {
				{TripID: "trip-freq", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 600, ExactTimes: 0},
			},
		}

		schedules, frequencies, _, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtxWithFreqs)
		assert.NoError(t, err)

		assert.Len(t, schedules["1_10"]["0"], 1, "only the non-frequency trip keeps a schedule stop time")
		assert.Equal(t, "1_trip-normal", schedules["1_10"]["0"][0].TripID)

		require.Len(t, frequencies["1_10"]["0"], 1, "the frequency trip gets one schedule frequency per frequency row")
		freqEntry := frequencies["1_10"]["0"][0]
		assert.Equal(t, "1_trip-freq", freqEntry.TripID)
		assert.Equal(t, startOfDay.Add(6*time.Hour).UnixMilli(), freqEntry.FrequencyWindow.StartTime.UnixMilli())
	})

	t.Run("expands exact_times=1 trips into discrete stop times", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-exact", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
		}
		rowCtxWithExactFreqs := rowCtx
		rowCtxWithExactFreqs.freqMap = map[string][]gtfsdb.Frequency{
			"trip-exact": {
				{TripID: "trip-exact", StartTime: int64(6 * time.Hour), EndTime: int64(9 * time.Hour), HeadwaySecs: 1800, ExactTimes: 1},
			},
		}

		schedules, frequencies, _, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtxWithExactFreqs)
		assert.NoError(t, err)

		// Window 06:00-09:00 at a 30-minute headway yields 6 template expansions.
		require.Len(t, schedules["1_10"]["0"], 6, "exact_times=1 trips are expanded per headway offset")
		for i, st := range schedules["1_10"]["0"] {
			expectedOffset := 6*time.Hour + time.Duration(i)*30*time.Minute
			assert.Equal(t, startOfDay.Add(expectedOffset).UnixMilli(), st.ArrivalTime)
			assert.Equal(t, "1_trip-exact", st.TripID)
		}
		assert.Empty(t, frequencies, "exact_times=1 trips produce no schedule frequencies")
	})
}

func TestScheduleForStopHandlerWithFrequency(t *testing.T) {
	api := createTestApiWithFrequencyData(t)
	defer api.Shutdown()

	startOfDay := time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)
	combinedStopID := utils.FormCombinedID(freqAgencyID, freqStopBID)
	endpoint := "/api/where/schedule-for-stop/" + combinedStopID + ".json?key=TEST&date=2025-06-12"

	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]any)
	entry := data["entry"].(map[string]any)
	schedules := entry["stopRouteSchedules"].([]any)
	require.Len(t, schedules, 1, "single frequency route in fixture")
	require.Len(t, schedules[0].(map[string]any)["stopRouteDirectionSchedules"].([]any), 1)

	dirSchedule := schedules[0].(map[string]any)["stopRouteDirectionSchedules"].([]any)[0].(map[string]any)

	stopTimesByTrip := make(map[string][]map[string]any)
	for _, stAny := range dirSchedule["scheduleStopTimes"].([]any) {
		st := stAny.(map[string]any)
		tripID := st["tripId"].(string)
		stopTimesByTrip[tripID] = append(stopTimesByTrip[tripID], st)
	}

	frequenciesByTrip := make(map[string][]map[string]any)
	for _, freqAny := range dirSchedule["scheduleFrequencies"].([]any) {
		freq := freqAny.(map[string]any)
		tripID := freq["tripId"].(string)
		frequenciesByTrip[tripID] = append(frequenciesByTrip[tripID], freq)
	}

	t.Run("headway-based trip appears only as scheduleFrequencies", func(t *testing.T) {
		combinedTripID := utils.FormCombinedID(freqAgencyID, freqTripID)
		freqEntries, ok := frequenciesByTrip[combinedTripID]
		require.True(t, ok, "expected a schedule frequency for the headway-based trip")
		require.Len(t, freqEntries, 1, "one frequency row per frequency trip")

		freqEntry := freqEntries[0]
		assert.Equal(t, float64(startOfDay.Add(6*time.Hour).UnixMilli()), freqEntry["startTime"])
		assert.Equal(t, float64(startOfDay.Add(9*time.Hour).UnixMilli()), freqEntry["endTime"])
		assert.Equal(t, float64(600), freqEntry["headway"])
		assert.Equal(t, float64(startOfDay.UnixMilli()), freqEntry["serviceDate"])
		assert.Equal(t, utils.FormCombinedID(freqAgencyID, freqServiceID), freqEntry["serviceId"])
		assert.Equal(t, combinedTripID, freqEntry["tripId"])
		assert.True(t, freqEntry["arrivalEnabled"].(bool), "second stop of the two-stop block has an arrival")
		assert.False(t, freqEntry["departureEnabled"].(bool), "last stop of the block has no onward departure")

		_, stillHasStopTime := stopTimesByTrip[combinedTripID]
		assert.False(t, stillHasStopTime, "headway-based trips have no template stop time in scheduleStopTimes")
	})

	t.Run("exact_times=1 trips expand into discrete stop times; plain trips keep theirs", func(t *testing.T) {
		assert.Len(t, stopTimesByTrip, 2, "exact_times=1 and plain trips both appear in scheduleStopTimes")

		exactTripID := utils.FormCombinedID(freqAgencyID, freqExactTripID)
		exactStopTimes := stopTimesByTrip[exactTripID]
		require.Len(t, exactStopTimes, 6, "window 06:00-09:00 at 30-minute headway yields 6 stop times")

		for i, st := range exactStopTimes {
			expected := startOfDay.Add(6*time.Hour + 15*time.Minute + time.Duration(i)*30*time.Minute).UnixMilli()
			assert.Equal(t, float64(expected), st["arrivalTime"])
			assert.Equal(t, float64(expected), st["departureTime"])
		}
		// Stop B is 15 minutes into the trip; block flags come from block position.
		assert.True(t, exactStopTimes[0]["arrivalEnabled"].(bool), "stop B is not the first block stop")
		assert.False(t, exactStopTimes[0]["departureEnabled"].(bool), "stop B is the block's last stop")
		assert.Empty(t, frequenciesByTrip[exactTripID], "expanded exact_times=1 trips produce no schedule frequencies")

		normalTripID := utils.FormCombinedID(freqAgencyID, freqNormalTripD)
		require.Len(t, stopTimesByTrip[normalTripID], 1, "plain trips keep their single template stop time")
		assert.Equal(t, float64(startOfDay.Add(8*time.Hour+15*time.Minute).UnixMilli()), stopTimesByTrip[normalTripID][0]["departureTime"])
		assert.Empty(t, frequenciesByTrip[normalTripID], "plain trips have no schedule frequencies")
	})
}
