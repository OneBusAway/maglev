package models

type RouteStopTime struct {
	ArrivalEnabled   bool   `json:"arrivalEnabled"`
	ArrivalTime      int    `json:"arrivalTime"`
	DepartureEnabled bool   `json:"departureEnabled"`
	DepartureTime    int    `json:"departureTime"`
	ServiceID        string `json:"serviceId"`
	StopHeadsign     string `json:"stopHeadsign"`
	StopID           string `json:"stopId"`
	TripID           string `json:"tripId"`
}

type TripStopTimes struct {
	TripID    string          `json:"tripId"`
	StopTimes []RouteStopTime `json:"stopTimes"`
}

type StopTripGrouping struct {
	DirectionID        int64           `json:"directionId"`
	TripHeadsigns      []string        `json:"tripHeadsigns"`
	StopIDs            []string        `json:"stopIds"`
	TripIDs            []string        `json:"tripIds"`
	TripsWithStopTimes []TripStopTimes `json:"tripsWithStopTimes"`
}

type ScheduleForRouteEntry struct {
	RouteID           string             `json:"routeId"`
	ScheduleDate      string             `json:"scheduleDate"`
	ServiceIDs        []string           `json:"serviceIds"`
	StopTripGroupings []StopTripGrouping `json:"stopTripGroupings"`
}
