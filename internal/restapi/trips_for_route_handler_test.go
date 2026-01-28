package restapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			routeID:      "25_1",
			minExpected:  0,
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
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true", tt.routeID)

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
	assert.Contains(t, trip, "frequency")
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
	assert.Contains(t, status, "frequency")
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
		assert.Contains(t, schedule, "frequency")
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
			url := fmt.Sprintf("/api/where/trips-for-route/25_1.json?key=TEST&includeSchedule=%v", tt.includeSchedule)

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
