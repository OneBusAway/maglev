package models

type TripsForRouteListEntry struct {
	Frequency    *Frequency     `json:"frequency"`
	Schedule     *TripsSchedule `json:"schedule,omitempty"`
	Status       *TripStatus    `json:"status,omitempty"`
	ServiceDate  int64          `json:"serviceDate"`
	SituationIds []string       `json:"situationIds"`
	TripId       string         `json:"tripId"`
}

func (e TripsForRouteListEntry) GetTripId() string { return e.TripId }
