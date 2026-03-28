package restapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTripsForLocationHandler_DifferentAreas(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name         string
		lat          float64
		lon          float64
		latSpan      float64
		lonSpan      float64
		minExpected  int
		maxExpected  int
		includeSpans bool
	}{
		{
			name:        "Transit Center Area",
			lat:         40.5865,
			lon:         -122.3917,
			latSpan:     1.0,
			lonSpan:     1.0,
			minExpected: 0,
			maxExpected: 50,
		},
		{
			name:        "Wide Area Coverage",
			lat:         40.5865,
			lon:         -122.3917,
			latSpan:     2,
			lonSpan:     3,
			minExpected: 0,
			maxExpected: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&latSpan=%f&lonSpan=%f&includeSchedule=true",
				tt.lat, tt.lon, tt.latSpan, tt.lonSpan)

			resp, model := serveApiAndRetrieveEndpoint(t, api, url)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data := model.Data.(map[string]interface{})
			list, ok := data["list"].([]interface{})
			assert.True(t, ok, "expected 'list' key in response data")

			for _, item := range list {
				trip, ok := item.(map[string]interface{})
				require.True(t, ok)

				assert.Contains(t, trip, "frequency")
				assert.Contains(t, trip, "serviceDate")
				assert.Contains(t, trip, "situationIds")
				assert.Contains(t, trip, "tripId")

				if schedule, hasSchedule := trip["schedule"].(map[string]interface{}); hasSchedule {
					assert.Contains(t, schedule, "frequency")
					assert.Contains(t, schedule, "nextTripId")
					assert.Contains(t, schedule, "previousTripId")
					assert.Contains(t, schedule, "timeZone")

					stopTimes, hasStopTimes := schedule["stopTimes"].([]interface{})
					if hasStopTimes {
						for _, st := range stopTimes {
							stopTime := st.(map[string]interface{})

							assert.Contains(t, stopTime, "arrivalTime")
							assert.Contains(t, stopTime, "departureTime")
							assert.Contains(t, stopTime, "stopId")
							assert.Contains(t, stopTime, "stopHeadsign")
							assert.Contains(t, stopTime, "distanceAlongTrip")
							assert.Contains(t, stopTime, "historicalOccupancy")

							assert.IsType(t, float64(0), stopTime["arrivalTime"])
							assert.IsType(t, float64(0), stopTime["departureTime"])
							assert.IsType(t, string(""), stopTime["stopId"])
							assert.IsType(t, float64(0), stopTime["distanceAlongTrip"])
						}
					}
				}

				assert.IsType(t, float64(0), trip["serviceDate"])
				assert.IsType(t, "", trip["tripId"])
				situationIds, ok := trip["situationIds"].([]interface{})
				require.True(t, ok)
				assert.IsType(t, []interface{}{}, situationIds)
			}

			assert.GreaterOrEqual(t, len(list), tt.minExpected)
			assert.LessOrEqual(t, len(list), tt.maxExpected)
		})
	}
}

func TestTripsForLocationHandler_ReferencesContainStopsAndRoutes(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name    string
		lat     float64
		lon     float64
		latSpan float64
		lonSpan float64
	}{
		{
			name:    "Transit Center Area",
			lat:     40.5865,
			lon:     -122.3917,
			latSpan: 1.0,
			lonSpan: 1.0,
		},
		{
			name:    "Wide Area Coverage",
			lat:     40.5865,
			lon:     -122.3917,
			latSpan: 2.0,
			lonSpan: 3.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&latSpan=%f&lonSpan=%f&includeSchedule=true",
				tt.lat, tt.lon, tt.latSpan, tt.lonSpan)

			resp, model := serveApiAndRetrieveEndpoint(t, api, url)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data, ok := model.Data.(map[string]interface{})
			require.True(t, ok, "response data should be a map")

			refs, ok := data["references"].(map[string]interface{})
			require.True(t, ok, "references should be present in response data")

			stops, ok := refs["stops"].([]interface{})
			require.True(t, ok, "references.stops should be a []interface{}, not null")

			routes, ok := refs["routes"].([]interface{})
			require.True(t, ok, "references.routes should be a []interface{}, not null")

			if len(stops) > 0 {
				routeIDSet := make(map[string]bool, len(routes))
				for _, r := range routes {
					route, ok := r.(map[string]interface{})
					require.True(t, ok, "each route in references.routes should be a map")
					if id, ok := route["id"].(string); ok {
						routeIDSet[id] = true
					}
				}

				for _, s := range stops {
					stop, ok := s.(map[string]interface{})
					require.True(t, ok, "each stop in references.stops should be a map")

					routeIds, ok := stop["routeIds"].([]interface{})
					assert.True(t, ok, "stop.routeIds should be a []interface{}, not null (stop: %v)", stop["id"])

					for _, rid := range routeIds {
						routeID, ok := rid.(string)
						require.True(t, ok)
						assert.True(t, routeIDSet[routeID],
							"route %q referenced by stop %q must appear in references.routes",
							routeID, stop["id"])
					}
				}
			}
		})
	}
}

func TestTripsForLocationHandler_ScheduleInclusion(t *testing.T) {
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
			url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=0.1&lonSpan=0.1&includeSchedule=%v",
				tt.includeSchedule)

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

func TestTripsForLocationMissingLat(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trips-for-location.json?key=TEST&lon=-122.426966&latSpan=0.01&lonSpan=0.01")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripsForLocationMissingLon(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trips-for-location.json?key=TEST&lat=40.583321&latSpan=0.01&lonSpan=0.01")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripsForLocationMissingBothLatAndLon(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/trips-for-location.json?key=TEST&latSpan=0.01&lonSpan=0.01")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripsForLocationHandler_StopIDsAreCombined(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := "/api/where/trips-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=2.0&lonSpan=3.0&includeSchedule=true"
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "references should be present")

	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok, "references.stops should be present")

	for _, s := range stops {
		stop, ok := s.(map[string]interface{})
		require.True(t, ok)
		id, ok := stop["id"].(string)
		require.True(t, ok, "stop id should be a string")
		assert.Contains(t, id, "_", "stop ID %q should be in {agencyID}_{rawID} format", id)
	}
}

func TestTripsForLocationHandler_OrphanedStopsNotInResponse(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := "/api/where/trips-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=2.0&lonSpan=3.0&includeSchedule=true"
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "references should be present")

	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok, "references.stops should be present")

	for _, s := range stops {
		stop, ok := s.(map[string]interface{})
		require.True(t, ok)
		id, ok := stop["id"].(string)
		require.True(t, ok, "stop id should be a string")
		assert.NotEmpty(t, id, "stop with empty ID found in response — orphaned stop slipped through")
	}
}

func TestTripsForLocationHandler_AgenciesExistForAllRoutes(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := "/api/where/trips-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=2.0&lonSpan=3.0&includeSchedule=true"
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "references should be present")

	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok, "references.agencies should be present")

	routes, ok := refs["routes"].([]interface{})
	require.True(t, ok, "references.routes should be present")

	agencyIDSet := make(map[string]bool, len(agencies))
	for _, a := range agencies {
		agency, ok := a.(map[string]interface{})
		require.True(t, ok)
		if id, ok := agency["id"].(string); ok {
			agencyIDSet[id] = true
		}
	}

	for _, r := range routes {
		route, ok := r.(map[string]interface{})
		require.True(t, ok)
		agencyID, ok := route["agencyId"].(string)
		require.True(t, ok, "route should have agencyId")
		assert.True(t, agencyIDSet[agencyID],
			"agency %q referenced by route %q must appear in references.agencies",
			agencyID, route["id"])
	}
}

func TestTripsForLocationHandler_StopDirectionsAreStrings(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := "/api/where/trips-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=2.0&lonSpan=3.0&includeSchedule=true"
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "references should be present")

	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok, "references.stops should be present")

	require.NotEmpty(t, stops, "expected stops in references to verify directions")

	nonEmptyCount := 0
	for _, s := range stops {
		stop, ok := s.(map[string]interface{})
		require.True(t, ok)
		_, ok = stop["direction"].(string)
		assert.True(t, ok, "stop %q direction field should be a string, not absent or null", stop["id"])
		if dir, _ := stop["direction"].(string); dir != "" {
			nonEmptyCount++
		}
	}
	assert.Greater(t, nonEmptyCount, 0, "at least some stops should have a non-empty direction from DirectionCalculator")
}

func TestTripsForLocationHandler_StatusInclusion(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name          string
		includeStatus bool
	}{
		{
			name:          "With Status",
			includeStatus: true,
		},
		{
			name:          "Without Status",
			includeStatus: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=0.1&lonSpan=0.1&includeStatus=%v",
				tt.includeStatus)

			resp, model := serveApiAndRetrieveEndpoint(t, api, url)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data := model.Data.(map[string]interface{})
			list := data["list"].([]interface{})

			assert.NotEmpty(t, list, "expected at least one trip in the response to verify status behavior")

			for _, item := range list {
				trip := item.(map[string]interface{})
				status, hasStatus := trip["status"].(map[string]interface{})

				if tt.includeStatus {
					assert.True(t, hasStatus, "expected status to be present when includeStatus=true")
					assert.NotNil(t, status)
					if status != nil {
						assert.Contains(t, status, "activeTripId")
						assert.Contains(t, status, "phase")
					}
				} else {
					assert.False(t, hasStatus, "expected status to be omitted when includeStatus=false")
				}
			}
		})
	}
}
