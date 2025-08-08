package models

type ArrivalAndDeparture struct {
	ActualTrack                string                    `json:"actualTrack"`
	ArrivalEnabled             bool                      `json:"arrivalEnabled"`
	BlockTripSequence          int                       `json:"blockTripSequence"`
	DepartureEnabled           bool                      `json:"departureEnabled"`
	DistanceFromStop           float64                   `json:"distanceFromStop"`
	Frequency                  *Frequency                `json:"frequency"`
	HistoricalOccupancy        string                    `json:"historicalOccupancy"`
	LastUpdateTime             int64                     `json:"lastUpdateTime"`
	NumberOfStopsAway          int                       `json:"numberOfStopsAway"`
	OccupancyStatus            string                    `json:"occupancyStatus"`
	Predicted                  bool                      `json:"predicted"`
	PredictedArrivalInterval   interface{}               `json:"predictedArrivalInterval"`
	PredictedArrivalTime       int64                     `json:"predictedArrivalTime"`
	PredictedDepartureInterval interface{}               `json:"predictedDepartureInterval"`
	PredictedDepartureTime     int64                     `json:"predictedDepartureTime"`
	PredictedOccupancy         string                    `json:"predictedOccupancy"`
	RouteID                    string                    `json:"routeId"`
	RouteLongName              string                    `json:"routeLongName"`
	RouteShortName             string                    `json:"routeShortName"`
	ScheduledArrivalInterval   interface{}               `json:"scheduledArrivalInterval"`
	ScheduledArrivalTime       int64                     `json:"scheduledArrivalTime"`
	ScheduledDepartureInterval interface{}               `json:"scheduledDepartureInterval"`
	ScheduledDepartureTime     int64                     `json:"scheduledDepartureTime"`
	ScheduledTrack             string                    `json:"scheduledTrack"`
	ServiceDate                int64                     `json:"serviceDate"`
	SituationIDs               []string                  `json:"situationIds"`
	Status                     string                    `json:"status"`
	StopID                     string                    `json:"stopId"`
	StopSequence               int                       `json:"stopSequence"`
	TotalStopsInTrip           int                       `json:"totalStopsInTrip"`
	TripHeadsign               string                    `json:"tripHeadsign"`
	TripID                     string                    `json:"tripId"`
	TripStatus                 *TripStatusForTripDetails `json:"tripStatus,omitempty"`
	VehicleID                  string                    `json:"vehicleId"`
}

func NewArrivalAndDeparture(
	routeID, routeShortName, routeLongName, tripID, tripHeadsign, stopID, vehicleID string,
	serviceDate, scheduledArrivalTime, scheduledDepartureTime, predictedArrivalTime, predictedDepartureTime, lastUpdateTime int64,
	predicted, arrivalEnabled, departureEnabled bool,
	stopSequence, totalStopsInTrip, numberOfStopsAway, blockTripSequence int,
	distanceFromStop float64,
	status, occupancyStatus, predictedOccupancy, historicalOccupancy string,
	tripStatus *TripStatusForTripDetails,
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
		LastUpdateTime:             lastUpdateTime,
		NumberOfStopsAway:          numberOfStopsAway,
		OccupancyStatus:            occupancyStatus,
		Predicted:                  predicted,
		PredictedArrivalInterval:   nil,
		PredictedArrivalTime:       predictedArrivalTime,
		PredictedDepartureInterval: nil,
		PredictedDepartureTime:     predictedDepartureTime,
		PredictedOccupancy:         predictedOccupancy,
		RouteID:                    routeID,
		RouteLongName:              routeLongName,
		RouteShortName:             routeShortName,
		ScheduledArrivalInterval:   nil,
		ScheduledArrivalTime:       scheduledArrivalTime,
		ScheduledDepartureInterval: nil,
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
