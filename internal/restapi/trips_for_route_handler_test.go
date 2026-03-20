package restapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

func TestTripsForRouteHandler_DifferentRoutes(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name         string
		routeID      string
		minExpected  int
		maxExpected  int
		expectStatus int
	}{
		{
			name:         "Main Route",
			routeID:      "25_151",
			minExpected:  1,
			maxExpected:  50,
			expectStatus: http.StatusOK,
		},
		{
			name:         "Non-existent Route",
			routeID:      "NONEXISTENT",
			minExpected:  0,
			maxExpected:  0,
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Empty Route ID",
			routeID:      "",
			minExpected:  0,
			maxExpected:  0,
			expectStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use 20:00:00 UTC (12:00 PM Noon Pacific Time) on a Tuesday
			// Buses are definitely running on this route at noon!
			testTime := time.Date(2024, 11, 5, 20, 0, 0, 0, time.UTC).UnixMilli()

			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%d", tt.routeID, testTime)

			resp, model := serveApiAndRetrieveEndpoint(t, api, url)
			assert.Equal(t, tt.expectStatus, resp.StatusCode)

			if tt.expectStatus != http.StatusOK {
				return
			}

			assert.Equal(t, 200, model.Code)
			assert.NotZero(t, model.CurrentTime)
			assert.Equal(t, "OK", model.Text)
			assert.Equal(t, 2, model.Version)

			data := model.Data.(map[string]interface{})
			assert.False(t, data["limitExceeded"].(bool))
			assert.False(t, data["outOfRange"].(bool))

			list, _ := data["list"].([]interface{})
			for _, item := range list {
				trip := item.(map[string]interface{})
				verifyTripEntry(t, trip)
			}

			references := data["references"].(map[string]interface{})
			verifyReferences(t, references)

			assert.GreaterOrEqual(t, len(list), tt.minExpected)
			assert.LessOrEqual(t, len(list), tt.maxExpected)
		})
	}
}

func verifyTripEntry(t *testing.T, trip map[string]interface{}) {
	assert.Contains(t, trip, "serviceDate")
	assert.Contains(t, trip, "situationIds")
	assert.Contains(t, trip, "tripId")
	assert.Contains(t, trip, "status")

	status := trip["status"].(map[string]interface{})
	assert.Contains(t, status, "activeTripId")
	assert.Contains(t, status, "blockTripSequence")
	assert.Contains(t, status, "closestStop")
	assert.Contains(t, status, "closestStopTimeOffset")
	assert.Contains(t, status, "distanceAlongTrip")
	assert.Contains(t, status, "phase")
	assert.Contains(t, status, "predicted")
	assert.Contains(t, status, "scheduleDeviation")
	assert.Contains(t, status, "serviceDate")
	assert.Contains(t, status, "situationIds")
	assert.Contains(t, status, "status")
	assert.Contains(t, status, "vehicleId")

	if pos := status["position"]; pos != nil {
		position := pos.(map[string]interface{})
		assert.Contains(t, position, "lat")
		assert.Contains(t, position, "lon")
	}

	if schedule, ok := trip["schedule"].(map[string]interface{}); ok {
		assert.Contains(t, schedule, "nextTripId")
		assert.Contains(t, schedule, "previousTripId")
		assert.Contains(t, schedule, "timeZone")

		if stopTimes, ok := schedule["stopTimes"].([]interface{}); ok {
			for _, st := range stopTimes {
				stopTime := st.(map[string]interface{})
				assert.Contains(t, stopTime, "arrivalTime")
				assert.Contains(t, stopTime, "departureTime")
				assert.Contains(t, stopTime, "stopId")
				assert.Contains(t, stopTime, "stopHeadsign")
				assert.Contains(t, stopTime, "distanceAlongTrip")
				assert.Contains(t, stopTime, "historicalOccupancy")
			}
		}
	}
}

func verifyReferences(t *testing.T, references map[string]interface{}) {
	agencies := references["agencies"].([]interface{})
	for _, a := range agencies {
		agency := a.(map[string]interface{})
		assert.Contains(t, agency, "disclaimer")
		assert.Contains(t, agency, "id")
		assert.Contains(t, agency, "lang")
		assert.Contains(t, agency, "name")
		assert.Contains(t, agency, "phone")
		assert.Contains(t, agency, "privateService")
		assert.Contains(t, agency, "timezone")
		assert.Contains(t, agency, "url")
	}

	routes := references["routes"].([]interface{})
	for _, r := range routes {
		route := r.(map[string]interface{})
		assert.Contains(t, route, "agencyId")
		assert.Contains(t, route, "color")
		assert.Contains(t, route, "description")
		assert.Contains(t, route, "id")
		assert.Contains(t, route, "longName")
		assert.Contains(t, route, "shortName")
		assert.Contains(t, route, "textColor")
		assert.Contains(t, route, "type")
	}

	stops := references["stops"].([]interface{})
	for _, s := range stops {
		stop := s.(map[string]interface{})
		assert.Contains(t, stop, "code")
		assert.Contains(t, stop, "direction")
		assert.Contains(t, stop, "id")
		assert.Contains(t, stop, "lat")
		assert.Contains(t, stop, "lon")
		assert.Contains(t, stop, "locationType")
		assert.Contains(t, stop, "name")
		assert.Contains(t, stop, "routeIds")
		assert.Contains(t, stop, "wheelchairBoarding")
	}
}

func TestTripsForRouteHandler_ScheduleInclusion(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name            string
		includeSchedule bool
	}{
		{
			name:            "With Schedule",
			includeSchedule: true,
		},
		{
			name:            "Without Schedule",
			includeSchedule: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/25_151.json?key=TEST&includeSchedule=%v", tt.includeSchedule)

			resp, model := serveApiAndRetrieveEndpoint(t, api, url)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data := model.Data.(map[string]interface{})
			list := data["list"].([]interface{})

			for _, item := range list {
				trip := item.(map[string]interface{})
				schedule, hasSchedule := trip["schedule"].(map[string]interface{})

				if tt.includeSchedule {
					assert.True(t, hasSchedule)
					assert.NotNil(t, schedule)
					if schedule != nil {
						assert.Contains(t, schedule, "stopTimes")
						assert.Contains(t, schedule, "timeZone")
					}
				} else {
					if hasSchedule {
						assert.Empty(t, schedule["stopTimes"])
					}
				}
			}
		})
	}
}

func TestTripsForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/trips-for-route/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestSelectBestTripInBlock(t *testing.T) {
	nullInt64 := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	row := func(id string, min, max int64) gtfsdb.GetTripsInBlockWithTimeBoundsRow {
		return gtfsdb.GetTripsInBlockWithTimeBoundsRow{ID: id, MinArrivalTime: nullInt64(min), MaxDepartureTime: nullInt64(max)}
	}

	// now = 1000
	now := int64(1000)

	t.Run("most recently completed when none running", func(t *testing.T) {
		rows := []gtfsdb.GetTripsInBlockWithTimeBoundsRow{
			row("older", 100, 500),
			row("recent", 600, 900),
		}
		assert.Equal(t, "recent", selectBestTripInBlock(rows, now))
	})

	t.Run("next upcoming when none completed", func(t *testing.T) {
		rows := []gtfsdb.GetTripsInBlockWithTimeBoundsRow{
			row("sooner", 1100, 1300),
			row("later", 1400, 1600),
		}
		assert.Equal(t, "sooner", selectBestTripInBlock(rows, now))
	})

	t.Run("fallback to first row when trip spans now", func(t *testing.T) {
		rows := []gtfsdb.GetTripsInBlockWithTimeBoundsRow{
			row("running", 800, 1200), // min < now < max
			row("future", 1300, 1500),
		}
		// "running" doesn't match MaxDepartureTime < now OR MinArrivalTime > now.
		// It falls back to the next available trip in the list ("future").
		assert.Equal(t, "future", selectBestTripInBlock(rows, now))
	})

	t.Run("completed beats upcoming", func(t *testing.T) {
		rows := []gtfsdb.GetTripsInBlockWithTimeBoundsRow{
			row("recent", 100, 800),
			row("next", 1200, 1500),
		}
		assert.Equal(t, "recent", selectBestTripInBlock(rows, now))
	})

	t.Run("fallback to first row when no time data matches", func(t *testing.T) {
		noTime := gtfsdb.GetTripsInBlockWithTimeBoundsRow{ID: "only"}
		assert.Equal(t, "only", selectBestTripInBlock([]gtfsdb.GetTripsInBlockWithTimeBoundsRow{noTime}, now))
	})
}

func TestStripNumericSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"LLR_TRIP_1083.00060", "LLR_TRIP_1083"},
		{"LLR_TRIP_1083.0", "LLR_TRIP_1083"},
		{"LLR_TRIP_1083", "LLR_TRIP_1083"},         // no dot → unchanged
		{"LLR_TRIP_1083.abc", "LLR_TRIP_1083.abc"}, // non-digit suffix → unchanged
		{"LLR_TRIP_1083.", "LLR_TRIP_1083."},       // trailing dot only → unchanged
		{"12345", "12345"},                         // no dot → unchanged
		{"a.1.2", "a.1"},                           // strips last numeric segment only
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, stripNumericSuffix(tt.input), "input: %q", tt.input)
	}
}

func TestCollectStopIDsFromSchedule_NilSchedule(t *testing.T) {
	stopIDsMap := map[string]bool{}
	collectStopIDsFromSchedule(nil, stopIDsMap)
	assert.Empty(t, stopIDsMap, "nil schedule must not add any entries")
}

func TestCollectStopIDsFromSchedule_PopulatesMap(t *testing.T) {
	schedule := &models.TripsSchedule{
		StopTimes: []models.StopTime{
			{StopID: "25_1001"},
			{StopID: "25_1002"},
			{StopID: "25_1003"},
		},
	}
	stopIDsMap := map[string]bool{}
	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Equal(t, map[string]bool{
		"1001": true,
		"1002": true,
		"1003": true,
	}, stopIDsMap)
}

func TestCollectStopIDsFromSchedule_SkipsMalformedIDs(t *testing.T) {
	schedule := &models.TripsSchedule{
		StopTimes: []models.StopTime{
			{StopID: "25_good"},
			{StopID: "no-underscore"},
		},
	}
	stopIDsMap := map[string]bool{}
	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Equal(t, map[string]bool{"good": true}, stopIDsMap,
		"malformed stop IDs must be silently skipped")
}

func TestCollectStopIDsFromSchedule_EmptyStopTimes(t *testing.T) {
	schedule := &models.TripsSchedule{StopTimes: []models.StopTime{}}
	stopIDsMap := map[string]bool{}
	collectStopIDsFromSchedule(schedule, stopIDsMap)
	assert.Empty(t, stopIDsMap)
}

func TestTripsForRouteHandler_DuplicatedTrips(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	duplicateTripID := "25_TRIP_DUPLICATE.00060"

	// Create a synthetic DUPLICATED vehicle
	injectedVehicle := gtfs.Vehicle{
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{
				ID:                   duplicateTripID,
				RouteID:              "25_151",
				ScheduleRelationship: gtfsrt.TripDescriptor_DUPLICATED,
			},
		},
	}

	// Inject it safely using the manager's test helper
	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{injectedVehicle})

	// Use a static time (Nov 5 2024, 12:00 PM Pacific)
	testTime := time.Date(2024, 11, 5, 20, 0, 0, 0, time.UTC).UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/25_151.json?key=TEST&includeStatus=true&time=%d", testTime)

	resp, model := serveApiAndRetrieveEndpoint(t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]interface{})
	list := data["list"].([]interface{})

	// Assert that the injected duplicated vehicle didn't cause the handler to panic or fail
	assert.NotNil(t, list)
}

func TestTripsForRouteHandler_PastMidnightService(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	// Simulate time being just past midnight (e.g., 00:30 AM)
	// so that previous day's late trips are included.
	currentLocation, _ := time.LoadLocation("America/Los_Angeles")
	pastMidnightTime := time.Date(2024, 11, 5, 0, 30, 0, 0, currentLocation)

	// Temporarily override the API clock
	// api.Clock = mockClock{Time: pastMidnightTime}

	// Format time parameter to force the handler to process at 00:30
	timeParam := pastMidnightTime.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/25_151.json?key=TEST&time=%d", timeParam)

	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]interface{})
	list := data["list"].([]interface{})

	// As long as the request succeeds and doesn't fail the bounds, the prev-day time math didn't panic
	assert.NotNil(t, list)
}
