package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTripDetails(t *testing.T) {
	trip := Trip{
		ID:             "trip_123",
		RouteID:        "route_456",
		ServiceID:      "service_789",
		TripHeadsign:   "Downtown Terminal",
		DirectionID:    1,
		BlockID:        "block_1",
		ShapeID:        "shape_1",
		TripShortName:  "DT",
		RouteShortName: "R1",
		PeakOffPeak:    1,
		TimeZone:       "America/Los_Angeles",
	}

	tripID := "trip_123"
	serviceDate := int64(1609459200000)

	frequency := &Frequency{
		StartTime: 28800,
		EndTime:   32400,
		Headway:   300,
	}

	status := &TripStatusForTripDetails{
		VehicleID: "vehicle_789",
		Status:    "in_progress",
	}

	schedule := &Schedule{
		Frequency:      600,
		NextTripID:     "next_trip",
		PreviousTripID: "prev_trip",
		StopTimes:      []StopTime{},
		TimeZone:       "America/Los_Angeles",
	}

	situationIDs := []string{"situation_1", "situation_2"}

	tripDetails := NewTripDetails(trip, tripID, serviceDate, frequency, status, schedule, situationIDs)

	assert.Equal(t, tripID, tripDetails.TripID)
	assert.Equal(t, serviceDate, tripDetails.ServiceDate)
	assert.Equal(t, frequency, tripDetails.Frequency)
	assert.Equal(t, status, tripDetails.Status)
	assert.Equal(t, schedule, tripDetails.Schedule)
	assert.Equal(t, situationIDs, tripDetails.SituationIDs)
}

func TestNewEmptyTripDetails(t *testing.T) {
	tripDetails := NewEmptyTripDetails()

	assert.Equal(t, "", tripDetails.TripID)
	assert.Equal(t, int64(0), tripDetails.ServiceDate)
	assert.Nil(t, tripDetails.Frequency)
	assert.Nil(t, tripDetails.Status)
	assert.Nil(t, tripDetails.Schedule)
	assert.NotNil(t, tripDetails.SituationIDs)
	assert.Empty(t, tripDetails.SituationIDs)
}

func TestTripDetailsJSON(t *testing.T) {
	frequency := &Frequency{
		StartTime: 28800,
		EndTime:   32400,
		Headway:   300,
	}

	status := &TripStatusForTripDetails{
		VehicleID: "vehicle_789",
		Status:    "in_progress",
	}

	schedule := &Schedule{
		Frequency:      600,
		NextTripID:     "next_trip",
		PreviousTripID: "prev_trip",
		StopTimes:      []StopTime{},
		TimeZone:       "America/Los_Angeles",
	}

	tripDetails := TripDetails{
		TripID:       "trip_123",
		ServiceDate:  1609459200000,
		Frequency:    frequency,
		Status:       status,
		Schedule:     schedule,
		SituationIDs: []string{"situation_1"},
	}

	jsonData, err := json.Marshal(tripDetails)
	assert.NoError(t, err)

	var unmarshaledTripDetails TripDetails
	err = json.Unmarshal(jsonData, &unmarshaledTripDetails)
	assert.NoError(t, err)

	assert.Equal(t, tripDetails.TripID, unmarshaledTripDetails.TripID)
	assert.Equal(t, tripDetails.ServiceDate, unmarshaledTripDetails.ServiceDate)
	assert.NotNil(t, unmarshaledTripDetails.Frequency)
	assert.NotNil(t, unmarshaledTripDetails.Status)
	assert.NotNil(t, unmarshaledTripDetails.Schedule)
	assert.Equal(t, tripDetails.SituationIDs, unmarshaledTripDetails.SituationIDs)
}

func TestTripDetailsWithNilValues(t *testing.T) {
	trip := Trip{ID: "trip_123"}

	tripDetails := NewTripDetails(trip, "trip_123", 1609459200000, nil, nil, nil, nil)

	assert.Equal(t, "trip_123", tripDetails.TripID)
	assert.Equal(t, int64(1609459200000), tripDetails.ServiceDate)
	assert.Nil(t, tripDetails.Frequency)
	assert.Nil(t, tripDetails.Status)
	assert.Nil(t, tripDetails.Schedule)
	assert.Nil(t, tripDetails.SituationIDs)
}

func TestTripStatusForTripDetailsJSON(t *testing.T) {
	tripStatus := TripStatusForTripDetails{
		ActiveTripID:               "active_trip_123",
		BlockTripSequence:          2,
		ClosestStop:                "stop_456",
		ClosestStopTimeOffset:      120,
		DistanceAlongTrip:          1500.5,
		Frequency:                  nil,
		LastKnownDistanceAlongTrip: 1400.0,
		LastKnownLocation: Location{
			Lat: 38.542661,
			Lon: -121.743914,
		},
		LastKnownOrientation:   90.0,
		LastLocationUpdateTime: 1609462700000,
		LastUpdateTime:         1609462800000,
		NextStop:               "stop_789",
		NextStopTimeOffset:     240,
		OccupancyCapacity:      50,
		OccupancyCount:         30,
		OccupancyStatus:        "MANY_SEATS_AVAILABLE",
		Orientation:            95.0,
		Phase:                  "in_progress",
		Position: Location{
			Lat: 38.543000,
			Lon: -121.744000,
		},
		Predicted:                  true,
		ScheduleDeviation:          60,
		ScheduledDistanceAlongTrip: 1450.0,
		ServiceDate:                1609459200000,
		SituationIDs:               []string{"situation_1"},
		Status:                     "SCHEDULED",
		TotalDistanceAlongTrip:     5000.0,
		VehicleFeatures:            []string{"wifi", "bike_rack"},
		VehicleID:                  "vehicle_789",
		Scheduled:                  true,
	}

	jsonData, err := json.Marshal(tripStatus)
	assert.NoError(t, err)

	var unmarshaledStatus TripStatusForTripDetails
	err = json.Unmarshal(jsonData, &unmarshaledStatus)
	assert.NoError(t, err)

	assert.Equal(t, tripStatus.VehicleID, unmarshaledStatus.VehicleID)
	assert.Equal(t, tripStatus.Status, unmarshaledStatus.Status)
	assert.Equal(t, tripStatus.Phase, unmarshaledStatus.Phase)
	assert.Equal(t, tripStatus.Predicted, unmarshaledStatus.Predicted)
	assert.Equal(t, tripStatus.Position.Lat, unmarshaledStatus.Position.Lat)
	assert.Equal(t, tripStatus.Position.Lon, unmarshaledStatus.Position.Lon)
}
