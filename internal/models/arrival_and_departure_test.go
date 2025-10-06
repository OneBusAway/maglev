package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewArrivalAndDeparture(t *testing.T) {
	routeID := "unitrans_FMS"
	routeShortName := "FMS"
	routeLongName := "Fremont Station"
	tripID := "trip_123"
	tripHeadsign := "Downtown Terminal"
	stopID := "stop_456"
	vehicleID := "vehicle_789"
	serviceDate := int64(1609459200000)
	scheduledArrivalTime := int64(1609462800000)
	scheduledDepartureTime := int64(1609462900000)
	predictedArrivalTime := int64(1609462850000)
	predictedDepartureTime := int64(1609462950000)
	lastUpdateTime := int64(1609462700000)
	predicted := true
	arrivalEnabled := true
	departureEnabled := true
	stopSequence := 5
	totalStopsInTrip := 20
	numberOfStopsAway := 3
	blockTripSequence := 2
	distanceFromStop := 500.75
	status := "SCHEDULED"
	occupancyStatus := "MANY_SEATS_AVAILABLE"
	predictedOccupancy := "FEW_SEATS_AVAILABLE"
	historicalOccupancy := "STANDING_ROOM_ONLY"
	tripStatus := &TripStatusForTripDetails{
		VehicleID: vehicleID,
		Status:    "in_progress",
	}
	situationIDs := []string{"situation_1", "situation_2"}

	arrival := NewArrivalAndDeparture(
		routeID, routeShortName, routeLongName, tripID, tripHeadsign, stopID, vehicleID,
		serviceDate, scheduledArrivalTime, scheduledDepartureTime, predictedArrivalTime, predictedDepartureTime, lastUpdateTime,
		predicted, arrivalEnabled, departureEnabled,
		stopSequence, totalStopsInTrip, numberOfStopsAway, blockTripSequence,
		distanceFromStop,
		status, occupancyStatus, predictedOccupancy, historicalOccupancy,
		tripStatus,
		situationIDs,
	)

	assert.Equal(t, routeID, arrival.RouteID)
	assert.Equal(t, routeShortName, arrival.RouteShortName)
	assert.Equal(t, routeLongName, arrival.RouteLongName)
	assert.Equal(t, tripID, arrival.TripID)
	assert.Equal(t, tripHeadsign, arrival.TripHeadsign)
	assert.Equal(t, stopID, arrival.StopID)
	assert.Equal(t, vehicleID, arrival.VehicleID)
	assert.Equal(t, serviceDate, arrival.ServiceDate)
	assert.Equal(t, scheduledArrivalTime, arrival.ScheduledArrivalTime)
	assert.Equal(t, scheduledDepartureTime, arrival.ScheduledDepartureTime)
	assert.Equal(t, predictedArrivalTime, arrival.PredictedArrivalTime)
	assert.Equal(t, predictedDepartureTime, arrival.PredictedDepartureTime)
	assert.Equal(t, lastUpdateTime, arrival.LastUpdateTime)
	assert.Equal(t, predicted, arrival.Predicted)
	assert.Equal(t, arrivalEnabled, arrival.ArrivalEnabled)
	assert.Equal(t, departureEnabled, arrival.DepartureEnabled)
	assert.Equal(t, stopSequence, arrival.StopSequence)
	assert.Equal(t, totalStopsInTrip, arrival.TotalStopsInTrip)
	assert.Equal(t, numberOfStopsAway, arrival.NumberOfStopsAway)
	assert.Equal(t, blockTripSequence, arrival.BlockTripSequence)
	assert.Equal(t, distanceFromStop, arrival.DistanceFromStop)
	assert.Equal(t, status, arrival.Status)
	assert.Equal(t, occupancyStatus, arrival.OccupancyStatus)
	assert.Equal(t, predictedOccupancy, arrival.PredictedOccupancy)
	assert.Equal(t, historicalOccupancy, arrival.HistoricalOccupancy)
	assert.Equal(t, tripStatus, arrival.TripStatus)
	assert.Equal(t, situationIDs, arrival.SituationIDs)
	assert.Equal(t, "", arrival.ActualTrack)
	assert.Equal(t, "", arrival.ScheduledTrack)
	assert.Nil(t, arrival.Frequency)
	assert.Nil(t, arrival.PredictedArrivalInterval)
	assert.Nil(t, arrival.PredictedDepartureInterval)
	assert.Nil(t, arrival.ScheduledArrivalInterval)
	assert.Nil(t, arrival.ScheduledDepartureInterval)
}

func TestArrivalAndDepartureJSON(t *testing.T) {
	tripStatus := &TripStatusForTripDetails{
		VehicleID: "vehicle_789",
		Status:    "in_progress",
	}

	arrival := ArrivalAndDeparture{
		ActualTrack:                "",
		ArrivalEnabled:             true,
		BlockTripSequence:          2,
		DepartureEnabled:           true,
		DistanceFromStop:           500.75,
		Frequency:                  nil,
		HistoricalOccupancy:        "STANDING_ROOM_ONLY",
		LastUpdateTime:             1609462700000,
		NumberOfStopsAway:          3,
		OccupancyStatus:            "MANY_SEATS_AVAILABLE",
		Predicted:                  true,
		PredictedArrivalInterval:   nil,
		PredictedArrivalTime:       1609462850000,
		PredictedDepartureInterval: nil,
		PredictedDepartureTime:     1609462950000,
		PredictedOccupancy:         "FEW_SEATS_AVAILABLE",
		RouteID:                    "unitrans_FMS",
		RouteLongName:              "Fremont Station",
		RouteShortName:             "FMS",
		ScheduledArrivalInterval:   nil,
		ScheduledArrivalTime:       1609462800000,
		ScheduledDepartureInterval: nil,
		ScheduledDepartureTime:     1609462900000,
		ScheduledTrack:             "",
		ServiceDate:                1609459200000,
		SituationIDs:               []string{"situation_1", "situation_2"},
		Status:                     "SCHEDULED",
		StopID:                     "stop_456",
		StopSequence:               5,
		TotalStopsInTrip:           20,
		TripHeadsign:               "Downtown Terminal",
		TripID:                     "trip_123",
		TripStatus:                 tripStatus,
		VehicleID:                  "vehicle_789",
	}

	jsonData, err := json.Marshal(arrival)
	assert.NoError(t, err)

	var unmarshaledArrival ArrivalAndDeparture
	err = json.Unmarshal(jsonData, &unmarshaledArrival)
	assert.NoError(t, err)

	assert.Equal(t, arrival.RouteID, unmarshaledArrival.RouteID)
	assert.Equal(t, arrival.RouteShortName, unmarshaledArrival.RouteShortName)
	assert.Equal(t, arrival.RouteLongName, unmarshaledArrival.RouteLongName)
	assert.Equal(t, arrival.TripID, unmarshaledArrival.TripID)
	assert.Equal(t, arrival.StopID, unmarshaledArrival.StopID)
	assert.Equal(t, arrival.VehicleID, unmarshaledArrival.VehicleID)
	assert.Equal(t, arrival.Status, unmarshaledArrival.Status)
	assert.Equal(t, arrival.Predicted, unmarshaledArrival.Predicted)
}

func TestArrivalAndDepartureWithEmptyValues(t *testing.T) {
	arrival := NewArrivalAndDeparture(
		"", "", "", "", "", "", "",
		0, 0, 0, 0, 0, 0,
		false, false, false,
		0, 0, 0, 0,
		0.0,
		"", "", "", "",
		nil,
		nil,
	)

	assert.Equal(t, "", arrival.RouteID)
	assert.Equal(t, "", arrival.RouteShortName)
	assert.Equal(t, "", arrival.RouteLongName)
	assert.Equal(t, "", arrival.TripID)
	assert.Equal(t, "", arrival.TripHeadsign)
	assert.Equal(t, "", arrival.StopID)
	assert.Equal(t, "", arrival.VehicleID)
	assert.Equal(t, int64(0), arrival.ServiceDate)
	assert.Equal(t, false, arrival.Predicted)
	assert.Equal(t, false, arrival.ArrivalEnabled)
	assert.Equal(t, false, arrival.DepartureEnabled)
	assert.Nil(t, arrival.TripStatus)
	assert.Nil(t, arrival.SituationIDs)
}

func TestArrivalAndDepartureWithNilTripStatus(t *testing.T) {
	arrival := NewArrivalAndDeparture(
		"route_1", "R1", "Route One", "trip_1", "Terminal", "stop_1", "vehicle_1",
		1609459200000, 1609462800000, 1609462900000, 1609462850000, 1609462950000, 1609462700000,
		true, true, true,
		1, 10, 2, 1,
		250.5,
		"SCHEDULED", "MANY_SEATS_AVAILABLE", "FEW_SEATS_AVAILABLE", "STANDING_ROOM_ONLY",
		nil,
		[]string{},
	)

	assert.Nil(t, arrival.TripStatus)
	assert.NotNil(t, arrival.SituationIDs)
	assert.Empty(t, arrival.SituationIDs)
}
