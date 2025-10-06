package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewScheduleStopTime(t *testing.T) {
	arrivalTime := int64(1609462800000)
	departureTime := int64(1609462900000)
	serviceID := "service_123"
	stopHeadsign := "Downtown Terminal"
	tripID := "trip_456"

	stopTime := NewScheduleStopTime(arrivalTime, departureTime, serviceID, stopHeadsign, tripID)

	assert.Equal(t, true, stopTime.ArrivalEnabled)
	assert.Equal(t, arrivalTime, stopTime.ArrivalTime)
	assert.Equal(t, true, stopTime.DepartureEnabled)
	assert.Equal(t, departureTime, stopTime.DepartureTime)
	assert.Equal(t, serviceID, stopTime.ServiceID)
	assert.Equal(t, stopHeadsign, stopTime.StopHeadsign)
	assert.Equal(t, tripID, stopTime.TripID)
}

func TestScheduleStopTimeJSON(t *testing.T) {
	stopTime := ScheduleStopTime{
		ArrivalEnabled:   true,
		ArrivalTime:      1609462800000,
		DepartureEnabled: true,
		DepartureTime:    1609462900000,
		ServiceID:        "service_123",
		StopHeadsign:     "Downtown Terminal",
		TripID:           "trip_456",
	}

	jsonData, err := json.Marshal(stopTime)
	assert.NoError(t, err)

	var unmarshaledStopTime ScheduleStopTime
	err = json.Unmarshal(jsonData, &unmarshaledStopTime)
	assert.NoError(t, err)

	assert.Equal(t, stopTime.ArrivalEnabled, unmarshaledStopTime.ArrivalEnabled)
	assert.Equal(t, stopTime.ArrivalTime, unmarshaledStopTime.ArrivalTime)
	assert.Equal(t, stopTime.DepartureEnabled, unmarshaledStopTime.DepartureEnabled)
	assert.Equal(t, stopTime.DepartureTime, unmarshaledStopTime.DepartureTime)
	assert.Equal(t, stopTime.ServiceID, unmarshaledStopTime.ServiceID)
	assert.Equal(t, stopTime.StopHeadsign, unmarshaledStopTime.StopHeadsign)
	assert.Equal(t, stopTime.TripID, unmarshaledStopTime.TripID)
}

func TestNewStopRouteDirectionSchedule(t *testing.T) {
	tripHeadsign := "Northbound to Terminal"
	stopTime1 := NewScheduleStopTime(1609462800000, 1609462900000, "service_1", "Downtown", "trip_1")
	stopTime2 := NewScheduleStopTime(1609463000000, 1609463100000, "service_1", "Uptown", "trip_2")
	stopTimes := []ScheduleStopTime{stopTime1, stopTime2}

	directionSchedule := NewStopRouteDirectionSchedule(tripHeadsign, stopTimes)

	assert.Equal(t, tripHeadsign, directionSchedule.TripHeadsign)
	assert.Equal(t, stopTimes, directionSchedule.ScheduleStopTimes)
	assert.NotNil(t, directionSchedule.ScheduleFrequencies)
	assert.Empty(t, directionSchedule.ScheduleFrequencies)
}

func TestStopRouteDirectionScheduleJSON(t *testing.T) {
	stopTime := NewScheduleStopTime(1609462800000, 1609462900000, "service_1", "Downtown", "trip_1")

	directionSchedule := StopRouteDirectionSchedule{
		ScheduleFrequencies: []interface{}{},
		ScheduleStopTimes:   []ScheduleStopTime{stopTime},
		TripHeadsign:        "Northbound to Terminal",
	}

	jsonData, err := json.Marshal(directionSchedule)
	assert.NoError(t, err)

	var unmarshaledSchedule StopRouteDirectionSchedule
	err = json.Unmarshal(jsonData, &unmarshaledSchedule)
	assert.NoError(t, err)

	assert.Equal(t, directionSchedule.TripHeadsign, unmarshaledSchedule.TripHeadsign)
	assert.Equal(t, 1, len(unmarshaledSchedule.ScheduleStopTimes))
	assert.Empty(t, unmarshaledSchedule.ScheduleFrequencies)
}

func TestNewStopRouteSchedule(t *testing.T) {
	routeID := "route_789"
	stopTime1 := NewScheduleStopTime(1609462800000, 1609462900000, "service_1", "Downtown", "trip_1")
	directionSchedule1 := NewStopRouteDirectionSchedule("Northbound", []ScheduleStopTime{stopTime1})
	directionSchedule2 := NewStopRouteDirectionSchedule("Southbound", []ScheduleStopTime{})
	directionSchedules := []StopRouteDirectionSchedule{directionSchedule1, directionSchedule2}

	routeSchedule := NewStopRouteSchedule(routeID, directionSchedules)

	assert.Equal(t, routeID, routeSchedule.RouteID)
	assert.Equal(t, directionSchedules, routeSchedule.StopRouteDirectionSchedules)
	assert.Equal(t, 2, len(routeSchedule.StopRouteDirectionSchedules))
}

func TestStopRouteScheduleJSON(t *testing.T) {
	stopTime := NewScheduleStopTime(1609462800000, 1609462900000, "service_1", "Downtown", "trip_1")
	directionSchedule := NewStopRouteDirectionSchedule("Northbound", []ScheduleStopTime{stopTime})

	routeSchedule := StopRouteSchedule{
		RouteID:                     "route_789",
		StopRouteDirectionSchedules: []StopRouteDirectionSchedule{directionSchedule},
	}

	jsonData, err := json.Marshal(routeSchedule)
	assert.NoError(t, err)

	var unmarshaledSchedule StopRouteSchedule
	err = json.Unmarshal(jsonData, &unmarshaledSchedule)
	assert.NoError(t, err)

	assert.Equal(t, routeSchedule.RouteID, unmarshaledSchedule.RouteID)
	assert.Equal(t, 1, len(unmarshaledSchedule.StopRouteDirectionSchedules))
}

func TestNewScheduleForStopEntry(t *testing.T) {
	stopID := "stop_123"
	date := int64(1609459200000)
	stopTime1 := NewScheduleStopTime(1609462800000, 1609462900000, "service_1", "Downtown", "trip_1")
	directionSchedule := NewStopRouteDirectionSchedule("Northbound", []ScheduleStopTime{stopTime1})
	routeSchedule1 := NewStopRouteSchedule("route_1", []StopRouteDirectionSchedule{directionSchedule})
	routeSchedule2 := NewStopRouteSchedule("route_2", []StopRouteDirectionSchedule{})
	routeSchedules := []StopRouteSchedule{routeSchedule1, routeSchedule2}

	scheduleEntry := NewScheduleForStopEntry(stopID, date, routeSchedules)

	assert.Equal(t, date, scheduleEntry.Date)
	assert.Equal(t, stopID, scheduleEntry.StopID)
	assert.Equal(t, routeSchedules, scheduleEntry.StopRouteSchedules)
	assert.Equal(t, 2, len(scheduleEntry.StopRouteSchedules))
}

func TestScheduleForStopEntryJSON(t *testing.T) {
	stopTime := NewScheduleStopTime(1609462800000, 1609462900000, "service_1", "Downtown", "trip_1")
	directionSchedule := NewStopRouteDirectionSchedule("Northbound", []ScheduleStopTime{stopTime})
	routeSchedule := NewStopRouteSchedule("route_1", []StopRouteDirectionSchedule{directionSchedule})

	scheduleEntry := ScheduleForStopEntry{
		Date:               1609459200000,
		StopID:             "stop_123",
		StopRouteSchedules: []StopRouteSchedule{routeSchedule},
	}

	jsonData, err := json.Marshal(scheduleEntry)
	assert.NoError(t, err)

	var unmarshaledEntry ScheduleForStopEntry
	err = json.Unmarshal(jsonData, &unmarshaledEntry)
	assert.NoError(t, err)

	assert.Equal(t, scheduleEntry.Date, unmarshaledEntry.Date)
	assert.Equal(t, scheduleEntry.StopID, unmarshaledEntry.StopID)
	assert.Equal(t, 1, len(unmarshaledEntry.StopRouteSchedules))
}

func TestScheduleStopTimeWithEmptyValues(t *testing.T) {
	stopTime := NewScheduleStopTime(0, 0, "", "", "")

	assert.Equal(t, true, stopTime.ArrivalEnabled)
	assert.Equal(t, int64(0), stopTime.ArrivalTime)
	assert.Equal(t, true, stopTime.DepartureEnabled)
	assert.Equal(t, int64(0), stopTime.DepartureTime)
	assert.Equal(t, "", stopTime.ServiceID)
	assert.Equal(t, "", stopTime.StopHeadsign)
	assert.Equal(t, "", stopTime.TripID)
}

func TestStopRouteDirectionScheduleWithEmptyStopTimes(t *testing.T) {
	directionSchedule := NewStopRouteDirectionSchedule("Northbound", []ScheduleStopTime{})

	assert.Equal(t, "Northbound", directionSchedule.TripHeadsign)
	assert.Empty(t, directionSchedule.ScheduleStopTimes)
	assert.Empty(t, directionSchedule.ScheduleFrequencies)
}

func TestStopRouteScheduleWithEmptyDirectionSchedules(t *testing.T) {
	routeSchedule := NewStopRouteSchedule("route_1", []StopRouteDirectionSchedule{})

	assert.Equal(t, "route_1", routeSchedule.RouteID)
	assert.Empty(t, routeSchedule.StopRouteDirectionSchedules)
}

func TestScheduleForStopEntryWithEmptyRouteSchedules(t *testing.T) {
	scheduleEntry := NewScheduleForStopEntry("stop_1", 1609459200000, []StopRouteSchedule{})

	assert.Equal(t, "stop_1", scheduleEntry.StopID)
	assert.Equal(t, int64(1609459200000), scheduleEntry.Date)
	assert.Empty(t, scheduleEntry.StopRouteSchedules)
}
