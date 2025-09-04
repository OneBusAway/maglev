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
	Frequency    *int64         `json:"frequency"`
	Schedule     *TripsSchedule `json:"schedule,omitempty"`
	ServiceDate  int64          `json:"serviceDate"`
	SituationIds []string       `json:"situationIds"`
	TripId       string         `json:"tripId"`
}

func (e TripsForLocationListEntry) GetTripId() string { return e.TripId }
