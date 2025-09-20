package models

type TripDetails struct {
	Frequency    *Frequency                `json:"frequency,omitempty"`
	Schedule     *Schedule                 `json:"schedule"`
	ServiceDate  int64                     `json:"serviceDate"`
	SituationIDs []string                  `json:"situationIds,omitempty"`
	Status       *TripStatusForTripDetails `json:"status,omitempty"`
	TripID       string                    `json:"tripId"`
}

func NewTripDetails(trip Trip, tripID string, serviceDate int64, frequency *Frequency, status *TripStatusForTripDetails, schedule *Schedule, situationIDs []string) *TripDetails {
	return &TripDetails{
		TripID:       tripID,
		ServiceDate:  serviceDate,
		Frequency:    frequency,
		Status:       status,
		Schedule:     schedule,
		SituationIDs: situationIDs,
	}

}

func NewEmptyTripDetails() *TripDetails {
	return &TripDetails{
		TripID:       "",
		ServiceDate:  0,
		Frequency:    nil,
		Status:       nil,
		Schedule:     nil,
		SituationIDs: []string{},
	}
}

type TripStatusForTripDetails struct {
	ActiveTripID               string     `json:"activeTripId"`
	BlockTripSequence          int        `json:"blockTripSequence"`
	ClosestStop                string     `json:"closestStop"`
	ClosestStopTimeOffset      int        `json:"closestStopTimeOffset"`
	DistanceAlongTrip          float64    `json:"distanceAlongTrip"`
	Frequency                  *Frequency `json:"frequency,omitempty"`
	LastKnownDistanceAlongTrip float64    `json:"lastKnownDistanceAlongTrip"`
	LastKnownLocation          Location   `json:"lastKnownLocation"`
	LastKnownOrientation       float64    `json:"lastKnownOrientation"`
	LastLocationUpdateTime     int64      `json:"lastLocationUpdateTime"`
	LastUpdateTime             int64      `json:"lastUpdateTime"`
	NextStop                   string     `json:"nextStop"`
	NextStopTimeOffset         int        `json:"nextStopTimeOffset"`
	OccupancyCapacity          int        `json:"occupancyCapacity"`
	OccupancyCount             int        `json:"occupancyCount"`
	OccupancyStatus            string     `json:"occupancyStatus"`
	Orientation                float64    `json:"orientation"`
	Phase                      string     `json:"phase"`
	Position                   Location   `json:"position"`
	Predicted                  bool       `json:"predicted"`
	ScheduleDeviation          int        `json:"scheduleDeviation"`
	ScheduledDistanceAlongTrip float64    `json:"scheduledDistanceAlongTrip"`
	ServiceDate                int64      `json:"serviceDate"`
	SituationIDs               []string   `json:"situationIds"`
	Status                     string     `json:"status"`
	TotalDistanceAlongTrip     float64    `json:"totalDistanceAlongTrip"`
	VehicleFeatures            []string   `json:"vehicleFeatures,omitempty"`
	VehicleID                  string     `json:"vehicleId"`
	Scheduled                  bool       `json:"scheduled"`
}
