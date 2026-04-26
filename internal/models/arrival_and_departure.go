package models

import (
	"time"
)

type ArrivalAndDeparture struct {
	ActualTrack                string      `json:"actualTrack"`
	ArrivalEnabled             bool        `json:"arrivalEnabled"`
	BlockTripSequence          int         `json:"blockTripSequence"`
	DepartureEnabled           bool        `json:"departureEnabled"`
	DistanceFromStop           float64     `json:"distanceFromStop"`
	Frequency                  *Frequency  `json:"frequency"`
	HistoricalOccupancy        string      `json:"historicalOccupancy"`
	LastUpdateTime             ModelTime   `json:"lastUpdateTime"`
	NumberOfStopsAway          int         `json:"numberOfStopsAway"`
	OccupancyStatus            string      `json:"occupancyStatus"`
	Predicted                  bool        `json:"predicted"`
	PredictedArrivalInterval   any         `json:"predictedArrivalInterval"`
	PredictedArrivalTime       ModelTime   `json:"predictedArrivalTime"`
	PredictedDepartureInterval any         `json:"predictedDepartureInterval"`
	PredictedDepartureTime     ModelTime   `json:"predictedDepartureTime"`
	PredictedOccupancy         string      `json:"predictedOccupancy"`
	RouteID                    string      `json:"routeId"`
	RouteLongName              string      `json:"routeLongName"`
	RouteShortName             string      `json:"routeShortName"`
	ScheduledArrivalInterval   any         `json:"scheduledArrivalInterval"`
	ScheduledArrivalTime       ModelTime   `json:"scheduledArrivalTime"`
	ScheduledDepartureInterval any         `json:"scheduledDepartureInterval"`
	ScheduledDepartureTime     ModelTime   `json:"scheduledDepartureTime"`
	ScheduledTrack             string      `json:"scheduledTrack"`
	ServiceDate                ModelTime   `json:"serviceDate"`
	SituationIDs               []string    `json:"situationIds"`
	Status                     string      `json:"status"`
	StopID                     string      `json:"stopId"`
	StopSequence               int         `json:"stopSequence"`
	TotalStopsInTrip           int         `json:"totalStopsInTrip"`
	TripHeadsign               string      `json:"tripHeadsign"`
	TripID                     string      `json:"tripId"`
	TripStatus                 *TripStatus `json:"tripStatus,omitempty"`
	VehicleID                  string      `json:"vehicleId"`
}

func NewArrivalAndDeparture(
	routeID, routeShortName, routeLongName, tripID, tripHeadsign, stopID, vehicleID string,
	serviceDate, scheduledArrivalTime, scheduledDepartureTime, predictedArrivalTime, predictedDepartureTime, lastUpdateTime time.Time,
	predicted, arrivalEnabled, departureEnabled bool,
	stopSequence, totalStopsInTrip, numberOfStopsAway, blockTripSequence int,
	distanceFromStop float64,
	status, occupancyStatus, predictedOccupancy, historicalOccupancy string,
	tripStatus *TripStatus,
	situationIDs []string,
) *ArrivalAndDeparture {
	return &ArrivalAndDeparture{
		ActualTrack:                "",
		ArrivalEnabled:             arrivalEnabled,
		BlockTripSequence:          blockTripSequence,
		DepartureEnabled:           departureEnabled,
		DistanceFromStop:           distanceFromStop,
		Frequency:                  nil,
		HistoricalOccupancy:        historicalOccupancy,
		LastUpdateTime:             NewModelTime(lastUpdateTime),
		NumberOfStopsAway:          numberOfStopsAway,
		OccupancyStatus:            occupancyStatus,
		Predicted:                  predicted,
		PredictedArrivalInterval:   nil,
		PredictedArrivalTime:       NewModelTime(predictedArrivalTime),
		PredictedDepartureInterval: nil,
		PredictedDepartureTime:     NewModelTime(predictedDepartureTime),
		PredictedOccupancy:         predictedOccupancy,
		RouteID:                    routeID,
		RouteLongName:              routeLongName,
		RouteShortName:             routeShortName,
		ScheduledArrivalInterval:   nil,
		ScheduledArrivalTime:       NewModelTime(scheduledArrivalTime),
		ScheduledDepartureInterval: nil,
		ScheduledDepartureTime:     NewModelTime(scheduledDepartureTime),
		ScheduledTrack:             "",
		ServiceDate:                NewModelTime(serviceDate),
		SituationIDs:               situationIDs,
		Status:                     status,
		StopID:                     stopID,
		StopSequence:               stopSequence,
		TotalStopsInTrip:           totalStopsInTrip,
		TripHeadsign:               tripHeadsign,
		TripID:                     tripID,
		TripStatus:                 tripStatus,
		VehicleID:                  vehicleID,
	}
}
