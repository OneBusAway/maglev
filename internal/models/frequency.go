package models

type Frequency struct {
	StartTime   int64  `json:"startTime"`
	EndTime     int64  `json:"endTime"`
	Headway     int    `json:"headway"`
	ServiceData int64  `json:"serviceData"`
	ServiceID   string `json:"serviceId"`
	TripID      string `json:"tripId"`
}
