package restapi

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
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

func TestScheduleForStopHandlerFrequencies(t *testing.T) {
	const targetDate = "20250612"
	const dateParam = "2025-06-12"

	findActiveScheduleRow := func(t *testing.T, api *RestAPI) (gtfsdb.Agency, gtfsdb.Stop, gtfsdb.GetScheduleForStopOnDateRow, time.Time) {
		t.Helper()
		agencies := mustGetAgencies(t, api)
		require.NotEmpty(t, agencies)
		agency := agencies[0]
		location, err := time.LoadLocation(agency.Timezone)
		require.NoError(t, err)
		startOfDay, err := time.ParseInLocation("20060102", targetDate, location)
		require.NoError(t, err)

		for _, stop := range mustGetStops(t, api) {
			routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(context.Background(), stop.ID)
			require.NoError(t, err)
			routeIDs := make([]string, 0, len(routes))
			for _, route := range routes {
				routeIDs = append(routeIDs, route.ID)
			}
			if len(routeIDs) == 0 {
				continue
			}

			rows, err := api.GtfsManager.GtfsDB.Queries.GetScheduleForStopOnDate(
				context.Background(),
				gtfsdb.GetScheduleForStopOnDateParams{
					TargetDate: targetDate,
					Weekday:    "thursday",
					StopID:     stop.ID,
					RouteIds:   routeIDs,
				},
			)
			require.NoError(t, err)
			if len(rows) > 0 {
				return agency, stop, rows[0], startOfDay
			}
		}

		t.Fatal("test feed has no active schedule row")
		return gtfsdb.Agency{}, gtfsdb.Stop{}, gtfsdb.GetScheduleForStopOnDateRow{}, time.Time{}
	}

	collectTripEntries := func(
		t *testing.T,
		model models.ResponseModel,
		combinedTripID string,
	) (frequencies, stopTimes []map[string]any) {
		t.Helper()
		require.Equal(t, http.StatusOK, model.Code)
		data, ok := model.Data.(map[string]any)
		require.True(t, ok)
		entry, ok := data["entry"].(map[string]any)
		require.True(t, ok)
		routeSchedules, ok := entry["stopRouteSchedules"].([]any)
		require.True(t, ok)

		for _, routeScheduleValue := range routeSchedules {
			routeSchedule := routeScheduleValue.(map[string]any)
			directions := routeSchedule["stopRouteDirectionSchedules"].([]any)
			for _, directionValue := range directions {
				direction := directionValue.(map[string]any)
				for _, frequencyValue := range direction["scheduleFrequencies"].([]any) {
					frequency := frequencyValue.(map[string]any)
					if frequency["tripId"] == combinedTripID {
						frequencies = append(frequencies, frequency)
					}
				}
				for _, stopTimeValue := range direction["scheduleStopTimes"].([]any) {
					stopTime := stopTimeValue.(map[string]any)
					if stopTime["tripId"] == combinedTripID {
						stopTimes = append(stopTimes, stopTime)
					}
				}
			}
		}
		return frequencies, stopTimes
	}
	replaceFrequenciesForTest := func(t *testing.T, api *RestAPI) {
		t.Helper()
		ctx := context.Background()
		tripIDs, err := api.GtfsManager.GtfsDB.Queries.GetFrequencyTripIDs(ctx)
		require.NoError(t, err)
		originalFrequencies, err := api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrips(ctx, tripIDs)
		require.NoError(t, err)
		require.NoError(t, api.GtfsManager.GtfsDB.Queries.ClearFrequencies(ctx))

		t.Cleanup(func() {
			cleanupCtx := context.Background()
			require.NoError(t, api.GtfsManager.GtfsDB.Queries.ClearFrequencies(cleanupCtx))
			for _, frequency := range originalFrequencies {
				require.NoError(t, api.GtfsManager.GtfsDB.Queries.CreateFrequency(
					cleanupCtx,
					gtfsdb.CreateFrequencyParams{
						TripID:      frequency.TripID,
						StartTime:   frequency.StartTime,
						EndTime:     frequency.EndTime,
						HeadwaySecs: frequency.HeadwaySecs,
						ExactTimes:  frequency.ExactTimes,
					},
				))
			}
		})
	}

	t.Run("exact_times=0 populates sorted schedule frequencies", func(t *testing.T) {
		api := createTestApi(t)
		defer api.Shutdown()
		agency, stop, row, startOfDay := findActiveScheduleRow(t, api)
		ctx := context.Background()
		replaceFrequenciesForTest(t, api)

		const headwaySecs = int64(900)
		headway := time.Duration(headwaySecs) * time.Second
		windows := [][2]int64{
			{row.FirstDepartureTime + int64(2*time.Hour), row.FirstDepartureTime + int64(2*time.Hour+2*headway)},
			{row.FirstDepartureTime, row.FirstDepartureTime + int64(2*headway)},
		}
		for _, window := range windows {
			require.NoError(t, api.GtfsManager.GtfsDB.Queries.CreateFrequency(ctx, gtfsdb.CreateFrequencyParams{
				TripID:      row.TripID,
				StartTime:   window[0],
				EndTime:     window[1],
				HeadwaySecs: headwaySecs,
				ExactTimes:  0,
			}))
		}

		stopID := utils.FormCombinedID(agency.ID, stop.ID)
		_, model := serveApiAndRetrieveEndpoint(
			t,
			api,
			"/api/where/schedule-for-stop/"+stopID+".json?key=TEST&date="+dateParam,
		)
		combinedTripID := utils.FormCombinedID(agency.ID, row.TripID)
		frequencies, stopTimes := collectTripEntries(t, model, combinedTripID)

		require.Len(t, frequencies, 2)
		assert.Empty(t, stopTimes, "the template trip must not also appear as fixed service")
		assert.Equal(t, float64(startOfDay.Add(time.Duration(windows[1][0])).UnixMilli()), frequencies[0]["startTime"])
		assert.Equal(t, float64(startOfDay.Add(time.Duration(windows[0][0])).UnixMilli()), frequencies[1]["startTime"])
		assert.Equal(t, float64(startOfDay.UnixMilli()), frequencies[0]["serviceDate"])
		assert.Equal(t, float64(headwaySecs), frequencies[0]["headway"])
		assert.Equal(t, utils.FormCombinedID(agency.ID, row.ServiceID), frequencies[0]["serviceId"])
		assert.IsType(t, true, frequencies[0]["arrivalEnabled"])
		assert.IsType(t, true, frequencies[0]["departureEnabled"])
	})

	t.Run("exact_times=1 expands deterministic stop times", func(t *testing.T) {
		api := createTestApi(t)
		defer api.Shutdown()
		agency, stop, row, startOfDay := findActiveScheduleRow(t, api)
		ctx := context.Background()
		replaceFrequenciesForTest(t, api)

		const headwaySecs = int64(1200)
		headway := time.Duration(headwaySecs) * time.Second
		require.NoError(t, api.GtfsManager.GtfsDB.Queries.CreateFrequency(ctx, gtfsdb.CreateFrequencyParams{
			TripID:      row.TripID,
			StartTime:   row.FirstDepartureTime,
			EndTime:     row.FirstDepartureTime + int64(2*headway),
			HeadwaySecs: headwaySecs,
			ExactTimes:  1,
		}))

		stopID := utils.FormCombinedID(agency.ID, stop.ID)
		_, model := serveApiAndRetrieveEndpoint(
			t,
			api,
			"/api/where/schedule-for-stop/"+stopID+".json?key=TEST&date="+dateParam,
		)
		combinedTripID := utils.FormCombinedID(agency.ID, row.TripID)
		frequencies, stopTimes := collectTripEntries(t, model, combinedTripID)

		assert.Empty(t, frequencies)
		require.Len(t, stopTimes, 2)
		assert.Equal(t, float64(startOfDay.Add(time.Duration(row.ArrivalTime)).UnixMilli()), stopTimes[0]["arrivalTime"])
		assert.Equal(t, float64(startOfDay.Add(time.Duration(row.DepartureTime)).UnixMilli()), stopTimes[0]["departureTime"])
		assert.Equal(t, float64(startOfDay.Add(time.Duration(row.ArrivalTime)+headway).UnixMilli()), stopTimes[1]["arrivalTime"])
		assert.Equal(t, float64(startOfDay.Add(time.Duration(row.DepartureTime)+headway).UnixMilli()), stopTimes[1]["departureTime"])
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
	withFrequency := func(
		row gtfsdb.GetScheduleForStopOnDateRow,
		startTime, endTime time.Duration,
		headwaySecs, exactTimes int64,
	) gtfsdb.GetScheduleForStopOnDateRow {
		row.FrequencyStartTime = nulls.Int64(int64(startTime))
		row.FrequencyEndTime = nulls.Int64(int64(endTime))
		row.FrequencyHeadwaySecs = nulls.Int64(headwaySecs)
		row.FrequencyExactTimes = nulls.Int64(exactTimes)
		return row
	}

	t.Run("splits rows on the same route into separate direction groups", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-out", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-in", "10", sql.NullInt64{Int64: 1, Valid: true}, "Uptown"),
		}

		schedules, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		routeGroups, ok := schedules["1_10"]
		assert.True(t, ok, "expected a group for route 1_10")
		assert.Len(t, routeGroups, 2, "expected two distinct direction buckets")

		assert.Len(t, routeGroups["0"].stopTimes, 1)
		assert.Equal(t, "1_trip-out", routeGroups["0"].stopTimes[0].TripID)

		assert.Len(t, routeGroups["1"].stopTimes, 1)
		assert.Equal(t, "1_trip-in", routeGroups["1"].stopTimes[0].TripID)
	})

	t.Run("groups rows on the same route and direction together", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-a", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-b", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
		}

		schedules, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		assert.Len(t, schedules["1_10"], 1, "expected a single direction bucket")
		assert.Len(t, schedules["1_10"]["0"].stopTimes, 2, "expected both stop times grouped together")
		assert.Equal(t, int64(2), schedules["1_10"]["0"].headsignCounts["Downtown"])
	})

	t.Run("defaults a missing direction_id to bucket 0", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-a", "10", sql.NullInt64{Valid: false}, "Downtown"),
		}

		schedules, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		assert.Len(t, schedules["1_10"], 1)
		assert.Contains(t, schedules["1_10"], "0")
		assert.Len(t, schedules["1_10"]["0"].stopTimes, 1)
	})

	t.Run("tracks headsign votes separately per direction", func(t *testing.T) {
		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-out-1", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-out-2", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
			makeRow("trip-in-1", "10", sql.NullInt64{Int64: 1, Valid: true}, "Uptown"),
		}

		schedules, err := groupScheduleRowsByRouteAndDirection(context.Background(), rows, rowCtx)
		assert.NoError(t, err)

		assert.Equal(t, int64(2), schedules["1_10"]["0"].headsignCounts["Downtown"])
		assert.Equal(t, int64(1), schedules["1_10"]["1"].headsignCounts["Uptown"])
		assert.Equal(t, int64(0), schedules["1_10"]["1"].headsignCounts["Downtown"], "direction 1 should not see direction 0's headsign votes")
	})

	t.Run("represents approximate service as a frequency instead of a template stop time", func(t *testing.T) {
		row := makeRow("trip-frequency", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown")
		row.StopHeadsign = sql.NullString{String: "Central", Valid: true}
		row.FirstDepartureTime = int64(7 * time.Hour)
		row = withFrequency(row, 6*time.Hour, 9*time.Hour, 600, 0)

		schedules, err := groupScheduleRowsByRouteAndDirection(context.Background(), []gtfsdb.GetScheduleForStopOnDateRow{row}, rowCtx)
		assert.NoError(t, err)

		group := schedules["1_10"]["0"]
		assert.Empty(t, group.stopTimes, "the GTFS template stop time must not leak into fixed schedules")
		if assert.Len(t, group.frequencies, 1) {
			frequency := group.frequencies[0]
			assert.Equal(t, startOfDay.Add(6*time.Hour), frequency.StartTime.Time)
			assert.Equal(t, startOfDay.Add(9*time.Hour), frequency.EndTime.Time)
			assert.Equal(t, 600*time.Second, frequency.Headway.Duration)
			assert.Equal(t, startOfDay, frequency.ServiceDate.Time)
			assert.Equal(t, "1_svc1", frequency.ServiceID)
			assert.Equal(t, "1_trip-frequency", frequency.TripID)
			assert.Equal(t, "Central", frequency.StopHeadsign)
			assert.True(t, frequency.ArrivalEnabled)
			assert.True(t, frequency.DepartureEnabled)
		}
		assert.Equal(t, int64(18), group.headsignCounts["Downtown"])
	})

	t.Run("expands exact service relative to the template first departure", func(t *testing.T) {
		row := makeRow("trip-exact", "10", sql.NullInt64{Int64: 1, Valid: true}, "Uptown")
		row.ArrivalTime = int64(8 * time.Hour)
		row.DepartureTime = int64(8*time.Hour + 2*time.Minute)
		row.FirstDepartureTime = int64(7 * time.Hour)
		row = withFrequency(row, 6*time.Hour, 7*time.Hour, 1800, 1)

		schedules, err := groupScheduleRowsByRouteAndDirection(context.Background(), []gtfsdb.GetScheduleForStopOnDateRow{row}, rowCtx)
		assert.NoError(t, err)

		group := schedules["1_10"]["1"]
		assert.Empty(t, group.frequencies)
		if assert.Len(t, group.stopTimes, 2) {
			assert.Equal(t, startOfDay.Add(7*time.Hour).UnixMilli(), group.stopTimes[0].ArrivalTime)
			assert.Equal(t, startOfDay.Add(7*time.Hour+2*time.Minute).UnixMilli(), group.stopTimes[0].DepartureTime)
			assert.Equal(t, startOfDay.Add(7*time.Hour+30*time.Minute).UnixMilli(), group.stopTimes[1].ArrivalTime)
			assert.Equal(t, startOfDay.Add(7*time.Hour+32*time.Minute).UnixMilli(), group.stopTimes[1].DepartureTime)
		}
		assert.Equal(t, int64(2), group.headsignCounts["Uptown"])
	})

	t.Run("frequency headsign weighting can select the representative headsign", func(t *testing.T) {
		fixedA := makeRow("trip-fixed-a", "10", sql.NullInt64{Int64: 0, Valid: true}, "Local")
		fixedB := makeRow("trip-fixed-b", "10", sql.NullInt64{Int64: 0, Valid: true}, "Local")
		frequency := makeRow("trip-frequency", "10", sql.NullInt64{Int64: 0, Valid: true}, "Express")
		frequency = withFrequency(frequency, 6*time.Hour, 8*time.Hour, 600, 0)

		schedules, err := groupScheduleRowsByRouteAndDirection(
			context.Background(),
			[]gtfsdb.GetScheduleForStopOnDateRow{fixedA, fixedB, frequency},
			rowCtx,
		)
		assert.NoError(t, err)
		assert.Equal(t, "Express", bestHeadsign(schedules["1_10"]["0"].headsignCounts))
	})

	t.Run("counts a frequency window shorter than its headway", func(t *testing.T) {
		row := makeRow("trip-frequency", "10", nulls.Int64(0), "Downtown")
		row = withFrequency(row, 6*time.Hour, 6*time.Hour+5*time.Minute, 600, 0)

		schedules, err := groupScheduleRowsByRouteAndDirection(
			context.Background(),
			[]gtfsdb.GetScheduleForStopOnDateRow{row},
			rowCtx,
		)
		require.NoError(t, err)
		assert.Equal(t, int64(1), schedules["1_10"]["0"].headsignCounts["Downtown"])
	})

	t.Run("rejects an invalid frequency before expansion", func(t *testing.T) {
		row := makeRow("trip-invalid", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown")
		row = withFrequency(row, 6*time.Hour, 7*time.Hour, 0, 1)

		_, err := groupScheduleRowsByRouteAndDirection(context.Background(), []gtfsdb.GetScheduleForStopOnDateRow{row}, rowCtx)
		assert.ErrorContains(t, err, "invalid frequency headway")
	})

	t.Run("returns an error when the context is already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		rows := []gtfsdb.GetScheduleForStopOnDateRow{
			makeRow("trip-a", "10", sql.NullInt64{Int64: 0, Valid: true}, "Downtown"),
		}

		_, err := groupScheduleRowsByRouteAndDirection(ctx, rows, rowCtx)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestBuildDirectionSchedulesUsesDirectionIDForHeadsignTies(t *testing.T) {
	directionMap := map[string]*scheduleDirectionGroup{
		"1": {
			stopTimes:      []models.ScheduleStopTime{{TripID: "trip-direction-1"}},
			headsignCounts: map[string]int64{"Downtown": 1},
		},
		"0": {
			stopTimes:      []models.ScheduleStopTime{{TripID: "trip-direction-0"}},
			headsignCounts: map[string]int64{"Downtown": 1},
		},
	}

	schedules := buildDirectionSchedules(directionMap)
	require.Len(t, schedules, 2)
	assert.Equal(t, "trip-direction-0", schedules[0].ScheduleStopTimes[0].TripID)
	assert.Equal(t, "trip-direction-1", schedules[1].ScheduleStopTimes[0].TripID)
}

func TestParseScheduleFrequencyRow(t *testing.T) {
	validRow := gtfsdb.GetScheduleForStopOnDateRow{
		TripID:               "trip-frequency",
		FrequencyStartTime:   nulls.Int64(int64(6 * time.Hour)),
		FrequencyEndTime:     nulls.Int64(int64(7 * time.Hour)),
		FrequencyHeadwaySecs: nulls.Int64(600),
		FrequencyExactTimes:  nulls.Int64(0),
	}

	tests := []struct {
		name        string
		mutate      func(*gtfsdb.GetScheduleForStopOnDateRow)
		wantPresent bool
		wantErr     string
	}{
		{
			name: "no frequency",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				row.FrequencyStartTime = sql.NullInt64{}
				row.FrequencyEndTime = sql.NullInt64{}
				row.FrequencyHeadwaySecs = sql.NullInt64{}
				row.FrequencyExactTimes = sql.NullInt64{}
			},
		},
		{
			name: "incomplete frequency",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				row.FrequencyEndTime = sql.NullInt64{}
			},
			wantErr: "incomplete frequency row",
		},
		{
			name: "negative start",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				row.FrequencyStartTime = nulls.Int64(-1)
			},
			wantErr: "invalid frequency window",
		},
		{
			name: "non-increasing window",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				row.FrequencyEndTime = row.FrequencyStartTime
			},
			wantErr: "invalid frequency window",
		},
		{
			name: "zero headway",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				row.FrequencyHeadwaySecs = nulls.Int64(0)
			},
			wantErr: "invalid frequency headway",
		},
		{
			name: "overflowing headway",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				const maxHeadwaySeconds = math.MaxInt64 / int64(time.Second)
				row.FrequencyHeadwaySecs = nulls.Int64(maxHeadwaySeconds + 1)
			},
			wantErr: "invalid frequency headway",
		},
		{
			name: "unsupported exact_times",
			mutate: func(row *gtfsdb.GetScheduleForStopOnDateRow) {
				row.FrequencyExactTimes = nulls.Int64(2)
			},
			wantErr: "invalid exact_times",
		},
		{
			name:        "valid frequency",
			mutate:      func(*gtfsdb.GetScheduleForStopOnDateRow) {},
			wantPresent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := validRow
			test.mutate(&row)

			frequency, present, err := parseScheduleFrequencyRow(row)
			if test.wantErr != "" {
				assert.ErrorContains(t, err, test.wantErr)
				assert.False(t, present)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.wantPresent, present)
			if present {
				assert.Equal(t, int64(6*time.Hour), frequency.startTime)
				assert.Equal(t, int64(7*time.Hour), frequency.endTime)
				assert.Equal(t, int64(600), frequency.headwaySecs)
				assert.Equal(t, int64(0), frequency.exactTimes)
			}
		})
	}
}

func TestExpandExactScheduleStopTimesValidation(t *testing.T) {
	row := gtfsdb.GetScheduleForStopOnDateRow{
		TripID:             "trip-frequency",
		ArrivalTime:        int64(7 * time.Hour),
		DepartureTime:      int64(7 * time.Hour),
		FirstDepartureTime: int64(6 * time.Hour),
	}
	rowCtx := scheduleRowContext{startOfDay: time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)}
	frequency := scheduleFrequencyRow{
		startTime:   int64(6 * time.Hour),
		endTime:     int64(7 * time.Hour),
		headwaySecs: 600,
		exactTimes:  1,
	}

	t.Run("rejects a negative first departure", func(t *testing.T) {
		invalidRow := row
		invalidRow.FirstDepartureTime = -1
		_, err := expandExactScheduleStopTimes(
			context.Background(), invalidRow, rowCtx, models.ScheduleStopTime{}, frequency,
			maxExpandedScheduleStopTimes,
		)
		assert.ErrorContains(t, err, "invalid first departure time")
	})

	t.Run("honors cancellation during expansion", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := expandExactScheduleStopTimes(
			ctx, row, rowCtx, models.ScheduleStopTime{}, frequency, maxExpandedScheduleStopTimes,
		)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("rejects expansions beyond the request limit", func(t *testing.T) {
		largeFrequency := frequency
		largeFrequency.headwaySecs = 1
		largeFrequency.endTime = largeFrequency.startTime +
			(maxExpandedScheduleStopTimes+1)*int64(time.Second)
		_, err := expandExactScheduleStopTimes(
			context.Background(),
			row,
			rowCtx,
			models.ScheduleStopTime{},
			largeFrequency,
			maxExpandedScheduleStopTimes,
		)
		assert.ErrorContains(t, err, "exceeds the 10000-stop-time request limit")
	})
}

func TestAddScheduleOffset(t *testing.T) {
	tests := []struct {
		name    string
		base    int64
		offset  int64
		want    int64
		wantErr string
	}{
		{name: "adds a positive offset", base: 10, offset: 5, want: 15},
		{name: "adds a negative offset", base: 10, offset: -5, want: 5},
		{name: "rejects overflow", base: math.MaxInt64, offset: 1, wantErr: "time overflow"},
		{name: "rejects underflow", base: math.MinInt64, offset: -1, wantErr: "time underflow"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := addScheduleOffset(test.base, test.offset)
			if test.wantErr != "" {
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
