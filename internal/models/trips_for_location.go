package models

type TripsForLocationResponse struct {
	Code        int64                `json:"code"`
	CurrentTime int64                `json:"currentTime"`
	Data        TripsForLocationData `json:"data"`
}

type TripsForLocationData struct {
	LimitExceeded bool                        `json:"limitExceeded"`
	List          []TripsForLocationListEntry `json:"list"`
}

type TripsForLocationListEntry struct {
	Frequency    *int64                    `json:"frequency"`
	Schedule     *TripsForLocationSchedule `json:"schedule,omitempty"`
	ServiceDate  int64                     `json:"serviceDate"`
	SituationIds []string                  `json:"situationIds"`
	TripId       string                    `json:"tripId"`
}

type TripsForLocationSchedule struct {
	Frequency      *int64                             `json:"frequency"`
	NextTripId     string                             `json:"nextTripId"`
	PreviousTripId string                             `json:"previousTripId"`
	StopTimes      []TripsForLocationScheduleStopTime `json:"stopTimes"`
	TimeZone       string                             `json:"timeZone"`
}

type TripsForLocationScheduleStopTime struct {
	ArrivalTime         int     `json:"arrivalTime"`
	DepartureTime       int     `json:"departureTime"`
	DistanceAlongTrip   float64 `json:"distanceAlongTrip"`
	HistoricalOccupancy string  `json:"historicalOccupancy"`
	StopHeadsign        string  `json:"stopHeadsign"`
	StopId              string  `json:"stopId"`
}
