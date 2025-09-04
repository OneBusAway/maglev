package models

type TripsForRouteResponse struct {
	Code        int64                `json:"code"`
	CurrentTime int64                `json:"currentTime"`
	Data        TripsForLocationData `json:"data"`
}

type TripsForRouteData struct {
	LimitExceeded bool                     `json:"limitExceeded"`
	List          []TripsForRouteListEntry `json:"list"`
}

type TripsForRouteListEntry struct {
	Frequency    *int64                    `json:"frequency"`
	Schedule     *TripsSchedule            `json:"schedule,omitempty"`
	Status       *TripStatusForTripDetails `json:"status,omitempty"`
	ServiceDate  int64                     `json:"serviceDate"`
	SituationIds []string                  `json:"situationIds"`
	TripId       string                    `json:"tripId"`
}

func (e TripsForRouteListEntry) GetTripId() string { return e.TripId }
