package models

type TripsForLocationResponse struct {
	Code        int                  `json:"code"`
	CurrentTime int64                `json:"currentTime"`
	Data        TripsForLocationData `json:"data"`
	Text        string               `json:"text"`
	Version     int                  `json:"version"`
}

type TripsForLocationData struct {
	LimitExceeded bool                        `json:"limitExceeded"`
	List          []TripsForLocationListEntry `json:"list"`
	OutOfRange    bool                        `json:"outOfRange"`
	References    ReferencesModel             `json:"references"`
}

type TripsForLocationListEntry struct {
	Frequency    *Frequency     `json:"frequency"`
	Schedule     *TripsSchedule `json:"schedule,omitempty"`
	Status       *TripStatus    `json:"status,omitempty"`
	ServiceDate  int64          `json:"serviceDate"`
	SituationIds []string       `json:"situationIds"`
	TripId       string         `json:"tripId"`
}

func (e TripsForLocationListEntry) GetTripId() string { return e.TripId }
