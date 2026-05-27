package models

import "time"

type StopTimes struct {
	StopTimes []StopTime `json:"stop_times"`
}

type StopTime struct {
	ArrivalTime         ModelDuration `json:"arrivalTime"`
	DepartureTime       ModelDuration `json:"departureTime"`
	DropOffType         int           `json:"dropOffType"`
	PickupType          int           `json:"pickupType"`
	StopID              string        `json:"stopId"`
	StopHeadsign        string        `json:"stopHeadsign"`
	DistanceAlongTrip   float64       `json:"distanceAlongTrip"`
	HistoricalOccupancy string        `json:"historicalOccupancy"`
}

func NewStopTime(arrivalTime, departureTime time.Duration, stopID, stopHeadsign string, distanceAlongTrip float64, historicalOccupancy string) StopTime {
	return StopTime{
		ArrivalTime:         NewModelDuration(arrivalTime),
		DepartureTime:       NewModelDuration(departureTime),
		StopID:              stopID,
		StopHeadsign:        stopHeadsign,
		DistanceAlongTrip:   distanceAlongTrip,
		HistoricalOccupancy: historicalOccupancy,
	}
}

func NewStopTimes(stopTimes []StopTime) StopTimes {
	return StopTimes{
		StopTimes: stopTimes,
	}
}
