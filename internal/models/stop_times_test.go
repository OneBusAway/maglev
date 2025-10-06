package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewStopTime(t *testing.T) {
	arrivalTime := 28800
	departureTime := 28900
	stopID := "unitrans_22005"
	stopHeadsign := "Downtown"
	distanceAlongTrip := 1234.56
	historicalOccupancy := "MANY_SEATS_AVAILABLE"

	stopTime := NewStopTime(arrivalTime, departureTime, stopID, stopHeadsign, distanceAlongTrip, historicalOccupancy)

	assert.Equal(t, arrivalTime, stopTime.ArrivalTime)
	assert.Equal(t, departureTime, stopTime.DepartureTime)
	assert.Equal(t, stopID, stopTime.StopID)
	assert.Equal(t, stopHeadsign, stopTime.StopHeadsign)
	assert.Equal(t, distanceAlongTrip, stopTime.DistanceAlongTrip)
	assert.Equal(t, historicalOccupancy, stopTime.HistoricalOccupancy)
}

func TestStopTimeJSON(t *testing.T) {
	stopTime := StopTime{
		ArrivalTime:         28800,
		DepartureTime:       28900,
		DropOffType:         0,
		PickupType:          0,
		StopID:              "unitrans_22005",
		StopHeadsign:        "Downtown",
		DistanceAlongTrip:   1234.56,
		HistoricalOccupancy: "MANY_SEATS_AVAILABLE",
	}

	jsonData, err := json.Marshal(stopTime)
	assert.NoError(t, err)

	var unmarshaledStopTime StopTime
	err = json.Unmarshal(jsonData, &unmarshaledStopTime)
	assert.NoError(t, err)

	assert.Equal(t, stopTime.ArrivalTime, unmarshaledStopTime.ArrivalTime)
	assert.Equal(t, stopTime.DepartureTime, unmarshaledStopTime.DepartureTime)
	assert.Equal(t, stopTime.StopID, unmarshaledStopTime.StopID)
	assert.Equal(t, stopTime.StopHeadsign, unmarshaledStopTime.StopHeadsign)
	assert.Equal(t, stopTime.DistanceAlongTrip, unmarshaledStopTime.DistanceAlongTrip)
	assert.Equal(t, stopTime.HistoricalOccupancy, unmarshaledStopTime.HistoricalOccupancy)
}

func TestStopTimeWithEmptyValues(t *testing.T) {
	stopTime := NewStopTime(0, 0, "", "", 0.0, "")

	assert.Equal(t, 0, stopTime.ArrivalTime)
	assert.Equal(t, 0, stopTime.DepartureTime)
	assert.Equal(t, "", stopTime.StopID)
	assert.Equal(t, "", stopTime.StopHeadsign)
	assert.Equal(t, 0.0, stopTime.DistanceAlongTrip)
	assert.Equal(t, "", stopTime.HistoricalOccupancy)
}

func TestNewStopTimes(t *testing.T) {
	stopTime1 := NewStopTime(28800, 28900, "stop_1", "Downtown", 100.0, "MANY_SEATS_AVAILABLE")
	stopTime2 := NewStopTime(29000, 29100, "stop_2", "Uptown", 200.0, "FEW_SEATS_AVAILABLE")

	stopTimes := NewStopTimes([]StopTime{stopTime1, stopTime2})

	assert.Equal(t, 2, len(stopTimes.StopTimes))
	assert.Equal(t, stopTime1, stopTimes.StopTimes[0])
	assert.Equal(t, stopTime2, stopTimes.StopTimes[1])
}

func TestStopTimesJSON(t *testing.T) {
	stopTime1 := NewStopTime(28800, 28900, "stop_1", "Downtown", 100.0, "MANY_SEATS_AVAILABLE")
	stopTime2 := NewStopTime(29000, 29100, "stop_2", "Uptown", 200.0, "FEW_SEATS_AVAILABLE")

	stopTimes := StopTimes{
		StopTimes: []StopTime{stopTime1, stopTime2},
	}

	jsonData, err := json.Marshal(stopTimes)
	assert.NoError(t, err)

	var unmarshaledStopTimes StopTimes
	err = json.Unmarshal(jsonData, &unmarshaledStopTimes)
	assert.NoError(t, err)

	assert.Equal(t, 2, len(unmarshaledStopTimes.StopTimes))
	assert.Equal(t, stopTimes.StopTimes[0].StopID, unmarshaledStopTimes.StopTimes[0].StopID)
	assert.Equal(t, stopTimes.StopTimes[1].StopID, unmarshaledStopTimes.StopTimes[1].StopID)
}

func TestStopTimesWithEmptyList(t *testing.T) {
	stopTimes := NewStopTimes([]StopTime{})

	assert.Empty(t, stopTimes.StopTimes)
	assert.Equal(t, 0, len(stopTimes.StopTimes))
}
