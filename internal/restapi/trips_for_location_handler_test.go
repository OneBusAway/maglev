package restapi

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			lonSpan:     2,
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
