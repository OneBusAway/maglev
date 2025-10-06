package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTripReference(t *testing.T) {
	id := "unitrans_trip_1"
	routeID := "unitrans_FMS"
	serviceID := "service_1"
	headSign := "Downtown Terminal"
	shortName := "FMS"
	directionID := int64(1)
	blockID := "block_1"
	shapeID := "shape_1"

	trip := NewTripReference(id, routeID, serviceID, headSign, shortName, directionID, blockID, shapeID)

	assert.Equal(t, id, trip.ID)
	assert.Equal(t, routeID, trip.RouteID)
	assert.Equal(t, serviceID, trip.ServiceID)
	assert.Equal(t, headSign, trip.TripHeadsign)
	assert.Equal(t, shortName, trip.TripShortName)
	assert.Equal(t, directionID, trip.DirectionID)
	assert.Equal(t, blockID, trip.BlockID)
	assert.Equal(t, shapeID, trip.ShapeID)
	assert.Equal(t, shortName, trip.RouteShortName)
	assert.Equal(t, int64(0), trip.PeakOffPeak)
	assert.Equal(t, "", trip.TimeZone)
}

func TestNewTripReferenceWithEmptyValues(t *testing.T) {
	trip := NewTripReference("", "", "", "", "", 0, "", "")

	assert.Equal(t, "", trip.ID)
	assert.Equal(t, "", trip.RouteID)
	assert.Equal(t, "", trip.ServiceID)
	assert.Equal(t, "", trip.TripHeadsign)
	assert.Equal(t, "", trip.TripShortName)
	assert.Equal(t, int64(0), trip.DirectionID)
	assert.Equal(t, "", trip.BlockID)
	assert.Equal(t, "", trip.ShapeID)
	assert.Equal(t, "", trip.RouteShortName)
	assert.Equal(t, int64(0), trip.PeakOffPeak)
	assert.Equal(t, "", trip.TimeZone)
}

func TestNewTripResponse(t *testing.T) {
	trip := &Trip{
		BlockID:        "block_1",
		DirectionID:    1,
		ID:             "unitrans_trip_1",
		RouteID:        "unitrans_FMS",
		ServiceID:      "service_1",
		ShapeID:        "shape_1",
		TripHeadsign:   "Downtown Terminal",
		TripShortName:  "FMS",
		RouteShortName: "FMS",
		PeakOffPeak:    0,
		TimeZone:       "",
	}
	timeZone := "America/Los_Angeles"
	peakOffPeak := 1

	tripResponse := NewTripResponse(trip, timeZone, peakOffPeak)

	assert.NotNil(t, tripResponse)
	assert.Equal(t, trip, tripResponse.Trip)
}

func TestTripJSON(t *testing.T) {
	trip := Trip{
		BlockID:        "block_1",
		DirectionID:    1,
		ID:             "unitrans_trip_1",
		RouteID:        "unitrans_FMS",
		ServiceID:      "service_1",
		ShapeID:        "shape_1",
		TripHeadsign:   "Downtown Terminal",
		TripShortName:  "FMS",
		RouteShortName: "FMS",
		PeakOffPeak:    1,
		TimeZone:       "America/Los_Angeles",
	}

	jsonData, err := json.Marshal(trip)
	assert.NoError(t, err)

	var unmarshaledTrip Trip
	err = json.Unmarshal(jsonData, &unmarshaledTrip)
	assert.NoError(t, err)

	assert.Equal(t, trip.BlockID, unmarshaledTrip.BlockID)
	assert.Equal(t, trip.DirectionID, unmarshaledTrip.DirectionID)
	assert.Equal(t, trip.ID, unmarshaledTrip.ID)
	assert.Equal(t, trip.RouteID, unmarshaledTrip.RouteID)
	assert.Equal(t, trip.ServiceID, unmarshaledTrip.ServiceID)
	assert.Equal(t, trip.ShapeID, unmarshaledTrip.ShapeID)
	assert.Equal(t, trip.TripHeadsign, unmarshaledTrip.TripHeadsign)
	assert.Equal(t, trip.TripShortName, unmarshaledTrip.TripShortName)
	assert.Equal(t, trip.RouteShortName, unmarshaledTrip.RouteShortName)
	assert.Equal(t, trip.PeakOffPeak, unmarshaledTrip.PeakOffPeak)
	assert.Equal(t, trip.TimeZone, unmarshaledTrip.TimeZone)
}

func TestTripResponseJSON(t *testing.T) {
	trip := &Trip{
		BlockID:        "block_1",
		DirectionID:    1,
		ID:             "unitrans_trip_1",
		RouteID:        "unitrans_FMS",
		ServiceID:      "service_1",
		ShapeID:        "shape_1",
		TripHeadsign:   "Downtown Terminal",
		TripShortName:  "FMS",
		RouteShortName: "FMS",
		PeakOffPeak:    1,
		TimeZone:       "America/Los_Angeles",
	}

	tripResponse := TripResponse{Trip: trip}

	jsonData, err := json.Marshal(tripResponse)
	assert.NoError(t, err)

	var unmarshaledTripResponse TripResponse
	err = json.Unmarshal(jsonData, &unmarshaledTripResponse)
	assert.NoError(t, err)

	assert.Equal(t, trip.ID, unmarshaledTripResponse.ID)
	assert.Equal(t, trip.RouteID, unmarshaledTripResponse.RouteID)
	assert.Equal(t, trip.TripHeadsign, unmarshaledTripResponse.TripHeadsign)
}

func TestTripsScheduleJSON(t *testing.T) {
	frequency := int64(300)
	schedule := TripsSchedule{
		Frequency:      &frequency,
		NextTripId:     "next_trip",
		PreviousTripId: "prev_trip",
		StopTimes: []StopTime{
			NewStopTime(28800, 28900, "stop_1", "Downtown", 100.0, "MANY_SEATS_AVAILABLE"),
		},
		TimeZone: "America/Los_Angeles",
	}

	jsonData, err := json.Marshal(schedule)
	assert.NoError(t, err)

	var unmarshaledSchedule TripsSchedule
	err = json.Unmarshal(jsonData, &unmarshaledSchedule)
	assert.NoError(t, err)

	assert.NotNil(t, unmarshaledSchedule.Frequency)
	assert.Equal(t, frequency, *unmarshaledSchedule.Frequency)
	assert.Equal(t, schedule.NextTripId, unmarshaledSchedule.NextTripId)
	assert.Equal(t, schedule.PreviousTripId, unmarshaledSchedule.PreviousTripId)
	assert.Equal(t, schedule.TimeZone, unmarshaledSchedule.TimeZone)
	assert.Equal(t, 1, len(unmarshaledSchedule.StopTimes))
}
