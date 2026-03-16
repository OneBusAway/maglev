package models

type ArrivalAndDeparture struct {
	ActualTrack                string      `json:"actualTrack"`
	ArrivalEnabled             bool        `json:"arrivalEnabled"`
	BlockTripSequence          int         `json:"blockTripSequence"`
	DepartureEnabled           bool        `json:"departureEnabled"`
	DistanceFromStop           float64     `json:"distanceFromStop"`
	Frequency                  string      `json:"frequency,omitempty"`
	HistoricalOccupancy        string      `json:"historicalOccupancy"`
	LastUpdateTime             *int64      `json:"lastUpdateTime,omitempty"`
	NumberOfStopsAway          int         `json:"numberOfStopsAway"`
	OccupancyStatus            string      `json:"occupancyStatus"`
	Predicted                  bool        `json:"predicted"`
	PredictedArrivalInterval   string      `json:"predictedArrivalInterval,omitempty"`
	PredictedArrivalTime       int64       `json:"predictedArrivalTime"`
	PredictedDepartureInterval string      `json:"predictedDepartureInterval,omitempty"`
	PredictedDepartureTime     int64       `json:"predictedDepartureTime"`
	PredictedOccupancy         string      `json:"predictedOccupancy"`
	RouteID                    string      `json:"routeId"`
	RouteLongName              string      `json:"routeLongName"`
	RouteShortName             string      `json:"routeShortName"`
	ScheduledArrivalInterval   string      `json:"scheduledArrivalInterval,omitempty"`
	ScheduledArrivalTime       int64       `json:"scheduledArrivalTime"`
	ScheduledDepartureInterval string      `json:"scheduledDepartureInterval,omitempty"`
	ScheduledDepartureTime     int64       `json:"scheduledDepartureTime"`
	ScheduledTrack             string      `json:"scheduledTrack"`
	ServiceDate                int64       `json:"serviceDate"`
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
	serviceDate, scheduledArrivalTime, scheduledDepartureTime, predictedArrivalTime, predictedDepartureTime int64,
	lastUpdateTime *int64,
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
		Frequency:                  "",
		HistoricalOccupancy:        historicalOccupancy,
		LastUpdateTime:             lastUpdateTime,
		NumberOfStopsAway:          numberOfStopsAway,
		OccupancyStatus:            occupancyStatus,
		Predicted:                  predicted,
		PredictedArrivalInterval:   "",
		PredictedArrivalTime:       predictedArrivalTime,
		PredictedDepartureInterval: "",
		PredictedDepartureTime:     predictedDepartureTime,
		PredictedOccupancy:         predictedOccupancy,
		RouteID:                    routeID,
		RouteLongName:              routeLongName,
		RouteShortName:             routeShortName,
		ScheduledArrivalInterval:   "",
		ScheduledArrivalTime:       scheduledArrivalTime,
		ScheduledDepartureInterval: "",
		ScheduledDepartureTime:     scheduledDepartureTime,
		ScheduledTrack:             "",
		ServiceDate:                serviceDate,
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
