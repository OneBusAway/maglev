package models

type Schedule struct {
	Frequency      *Frequency `json:"frequency"`
	NextTripID     string     `json:"nextTripId"`
	PreviousTripID string     `json:"previousTripId"`
	StopTimes      []StopTime `json:"stopTimes"`
	TimeZone       string     `json:"timeZone"`
}

func NewSchedule(frequency *Frequency, nextTripID, previousTripID string, stopTimes []StopTime, timeZone string) *Schedule {
	return &Schedule{
		Frequency:      frequency,
		NextTripID:     nextTripID,
		PreviousTripID: previousTripID,
		StopTimes:      stopTimes,
		TimeZone:       timeZone,
	}
}
