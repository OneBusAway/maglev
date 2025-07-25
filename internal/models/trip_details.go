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
	ServiceDate                int64    `json:"serviceDate"`
	ActiveTripID               string   `json:"activeTripId"`
	Phase                      string   `json:"phase"`
	Status                     string   `json:"status"`
	Predicted                  bool     `json:"predicted"`
	VehicleID                  string   `json:"vehicleId"`
	Position                   Location `json:"position"`
	LastKnownLocation          Location `json:"lastKnownLocation"`
	Orientation                float64  `json:"orientation"`
	LastKnownOrientation       float64  `json:"lastKnownOrientation"`
	ScheduleDeviation          int      `json:"scheduleDeviation"`
	DistanceAlongTrip          float64  `json:"distanceAlongTrip"`
	ScheduledDistanceAlongTrip float64  `json:"scheduledDistanceAlongTrip"`
	TotalDistanceAlongTrip     float64  `json:"totalDistanceAlongTrip"`
	LastKnownDistanceAlongTrip float64  `json:"lastKnownDistanceAlongTrip"`
	LastUpdateTime             int64    `json:"lastUpdateTime"`
	LastLocationUpdateTime     int64    `json:"lastLocationUpdateTime"`
	BlockTripSequence          int      `json:"blockTripSequence"`
	ClosestStop                string   `json:"closestStop"`
	ClosestStopTimeOffset      int      `json:"closestStopTimeOffset"`
	NextStop                   string   `json:"nextStop"`
	NextStopTimeOffset         int      `json:"nextStopTimeOffset"`
	OccupancyStatus            string   `json:"occupancyStatus"`
	OccupancyCount             int      `json:"occupancyCount"`
	OccupancyCapacity          int      `json:"occupancyCapacity"`
	SituationIDs               []string `json:"situationIds"`
	Scheduled                  bool     `json:"scheduled"`
}
