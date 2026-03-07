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
	ClosestStop                string     `json:"closestStop,omitempty"`
	ClosestStopTimeOffset      *int       `json:"closestStopTimeOffset,omitempty"`
	DistanceAlongTrip          *float64   `json:"distanceAlongTrip,omitempty"`
	Frequency                  *Frequency `json:"frequency,omitempty"`
	LastKnownDistanceAlongTrip *float64   `json:"lastKnownDistanceAlongTrip,omitempty"`
	LastKnownLocation          *Location  `json:"lastKnownLocation,omitempty"`
	LastKnownOrientation       *float64   `json:"lastKnownOrientation,omitempty"`
	LastLocationUpdateTime     *int64     `json:"lastLocationUpdateTime,omitempty"`
	LastUpdateTime             *int64     `json:"lastUpdateTime,omitempty"`
	NextStop                   string     `json:"nextStop,omitempty"`
	NextStopTimeOffset         *int       `json:"nextStopTimeOffset,omitempty"`
	OccupancyCapacity          *int       `json:"occupancyCapacity,omitempty"`
	OccupancyCount             *int       `json:"occupancyCount,omitempty"`
	OccupancyStatus            string     `json:"occupancyStatus,omitempty"`
	Orientation                *float64   `json:"orientation,omitempty"`
	Phase                      string     `json:"phase"`
	Position                   Location   `json:"position"`
	Predicted                  bool       `json:"predicted"`
	ScheduleDeviation          *int       `json:"scheduleDeviation,omitempty"`
	ScheduledDistanceAlongTrip *float64   `json:"scheduledDistanceAlongTrip,omitempty"`
	ServiceDate                int64      `json:"serviceDate"`
	SituationIDs               []string   `json:"situationIds,omitempty"`
	Status                     string     `json:"status"`
	TotalDistanceAlongTrip     *float64   `json:"totalDistanceAlongTrip,omitempty"`
	VehicleFeatures            []string   `json:"vehicleFeatures,omitempty"`
	VehicleID                  string     `json:"vehicleId,omitempty"`
	Scheduled                  bool       `json:"scheduled"` // (Scheduled = !Predicted) ,this field is not part of the OpenAPI TripStatus schema but is retained for compatibility with existing API consumers. Tracked as a known spec deviation.
}

// NewTripStatus returns a TripStatus with safe zero-value defaults.
// Always use this instead of &TripStatus{} to ensure SituationIDs is
// initialized to []string{} (never nil), avoiding null vs [] inconsistency in JSON.
func NewTripStatus() *TripStatus {
	return &TripStatus{
		SituationIDs: []string{},
	}
}
