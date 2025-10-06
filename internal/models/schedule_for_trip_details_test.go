package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSchedule(t *testing.T) {
	frequency := int64(300)
	nextTripID := "next_trip_123"
	previousTripID := "prev_trip_456"
	stopTime1 := NewStopTime(28800, 28900, "stop_1", "Downtown", 100.0, "MANY_SEATS_AVAILABLE")
	stopTime2 := NewStopTime(29000, 29100, "stop_2", "Uptown", 200.0, "FEW_SEATS_AVAILABLE")
	stopTimes := []StopTime{stopTime1, stopTime2}
	timeZone := "America/Los_Angeles"

	schedule := NewSchedule(frequency, nextTripID, previousTripID, stopTimes, timeZone)

	assert.Equal(t, frequency, schedule.Frequency)
	assert.Equal(t, nextTripID, schedule.NextTripID)
	assert.Equal(t, previousTripID, schedule.PreviousTripID)
	assert.Equal(t, stopTimes, schedule.StopTimes)
	assert.Equal(t, timeZone, schedule.TimeZone)
	assert.Equal(t, 2, len(schedule.StopTimes))
}

func TestScheduleJSON(t *testing.T) {
	stopTime := NewStopTime(28800, 28900, "stop_1", "Downtown", 100.0, "MANY_SEATS_AVAILABLE")

	schedule := Schedule{
		Frequency:      300,
		NextTripID:     "next_trip",
		PreviousTripID: "prev_trip",
		StopTimes:      []StopTime{stopTime},
		TimeZone:       "America/Los_Angeles",
	}

	jsonData, err := json.Marshal(schedule)
	assert.NoError(t, err)

	var unmarshaledSchedule Schedule
	err = json.Unmarshal(jsonData, &unmarshaledSchedule)
	assert.NoError(t, err)

	assert.Equal(t, schedule.Frequency, unmarshaledSchedule.Frequency)
	assert.Equal(t, schedule.NextTripID, unmarshaledSchedule.NextTripID)
	assert.Equal(t, schedule.PreviousTripID, unmarshaledSchedule.PreviousTripID)
	assert.Equal(t, schedule.TimeZone, unmarshaledSchedule.TimeZone)
	assert.Equal(t, 1, len(unmarshaledSchedule.StopTimes))
	assert.Equal(t, schedule.StopTimes[0].StopID, unmarshaledSchedule.StopTimes[0].StopID)
}

func TestScheduleWithEmptyValues(t *testing.T) {
	schedule := NewSchedule(0, "", "", []StopTime{}, "")

	assert.Equal(t, int64(0), schedule.Frequency)
	assert.Equal(t, "", schedule.NextTripID)
	assert.Equal(t, "", schedule.PreviousTripID)
	assert.Empty(t, schedule.StopTimes)
	assert.Equal(t, "", schedule.TimeZone)
}

func TestScheduleWithMultipleStopTimes(t *testing.T) {
	stopTimes := []StopTime{
		NewStopTime(28800, 28900, "stop_1", "Downtown", 100.0, "MANY_SEATS_AVAILABLE"),
		NewStopTime(29000, 29100, "stop_2", "Uptown", 200.0, "FEW_SEATS_AVAILABLE"),
		NewStopTime(29200, 29300, "stop_3", "Midtown", 300.0, "STANDING_ROOM_ONLY"),
	}

	schedule := NewSchedule(600, "trip_next", "trip_prev", stopTimes, "America/New_York")

	assert.Equal(t, 3, len(schedule.StopTimes))
	assert.Equal(t, "stop_1", schedule.StopTimes[0].StopID)
	assert.Equal(t, "stop_2", schedule.StopTimes[1].StopID)
	assert.Equal(t, "stop_3", schedule.StopTimes[2].StopID)
}
