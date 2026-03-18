package models

type TripDetails struct {
	Frequency    *Frequency  `json:"frequency"`
	Schedule     *Schedule   `json:"schedule"`
	ServiceDate  int64       `json:"serviceDate"`
	SituationIDs []string    `json:"situationIds"`
	Status       *TripStatus `json:"status,omitempty"`
	TripID       string      `json:"tripId"`
}

func NewTripDetails(trip Trip, tripID string, serviceDate int64, frequency *Frequency, status *TripStatus, schedule *Schedule, situationIDs []string) *TripDetails {
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

type TripStatus struct {
	ActiveTripID               string     `json:"activeTripId"`
	BlockTripSequence          int        `json:"blockTripSequence"`
	ClosestStop                string     `json:"closestStop"`
	ClosestStopTimeOffset      *int       `json:"closestStopTimeOffset,omitempty"`
	DistanceAlongTrip          float64    `json:"distanceAlongTrip"`
	Frequency                  *Frequency `json:"frequency,omitempty"`
	LastKnownDistanceAlongTrip float64    `json:"lastKnownDistanceAlongTrip"`
	LastKnownLocation          *Location  `json:"lastKnownLocation"`
	LastKnownOrientation       *float64   `json:"lastKnownOrientation,omitempty"`
	LastLocationUpdateTime     int64      `json:"lastLocationUpdateTime"`
	LastUpdateTime             int64      `json:"lastUpdateTime"`
	NextStop                   string     `json:"nextStop,omitempty"`
	NextStopTimeOffset         *int       `json:"nextStopTimeOffset,omitempty"`
	OccupancyCapacity          int        `json:"occupancyCapacity"`
	OccupancyCount             int        `json:"occupancyCount"`
	OccupancyStatus            string     `json:"occupancyStatus"`
	Orientation                *float64   `json:"orientation,omitempty"`
	Phase                      string     `json:"phase"`
	Position                   Location   `json:"position"`
	Predicted                  bool       `json:"predicted"`
	ScheduleDeviation          int        `json:"scheduleDeviation"`
	ScheduledDistanceAlongTrip *float64   `json:"scheduledDistanceAlongTrip,omitempty"`
	ServiceDate                int64      `json:"serviceDate"`
	SituationIDs               []string   `json:"situationIds,omitempty"`
	Status                     string     `json:"status"`
	TotalDistanceAlongTrip     float64    `json:"totalDistanceAlongTrip"`
	VehicleFeatures            []string   `json:"vehicleFeatures,omitempty"`
	VehicleID                  string     `json:"vehicleId,omitempty"`
	Scheduled                  bool       `json:"scheduled"` // (Scheduled = !Predicted) ,this field is not part of the OpenAPI TripStatus schema but is retained for compatibility with existing API consumers. This is a known deviation from the OpenAPI spec.
}

// SetPredicted keeps Predicted and Scheduled logically consistent.
func (ts *TripStatus) SetPredicted(predicted bool) {
	ts.Predicted = predicted
	ts.Scheduled = !predicted
}

// SituationIDs initialized to []string{} for Go-side convenience.
func NewTripStatus() *TripStatus {
	status := &TripStatus{
		SituationIDs: []string{},
	}
	status.SetPredicted(false)

	return status
}
