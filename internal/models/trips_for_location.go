package models

type TripsForLocationListEntry struct {
	Frequency    *Frequency     `json:"frequency"`
	Schedule     *TripsSchedule `json:"schedule,omitempty"`
	Status       *TripStatus    `json:"status,omitempty"`
	ServiceDate  int64          `json:"serviceDate"`
	SituationIds []string       `json:"situationIds"`
	TripId       string         `json:"tripId"`
}

func (e TripsForLocationListEntry) GetTripId() string { return e.TripId }
