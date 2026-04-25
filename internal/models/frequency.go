package models

import (
	"time"

	"maglev.onebusaway.org/gtfsdb"
)

// Frequency represents a GTFS frequency entry in API responses.
type Frequency struct {
	StartTime ModelTime `json:"startTime"`
	EndTime   ModelTime `json:"endTime"`
	// Headway is the time between departures.
	Headway ModelDuration `json:"headway"`
	// ExactTimes is used internally for business logic but omitted from API responses
	ExactTimes  int       `json:"-"`
	ServiceDate ModelTime `json:"serviceDate"`
	ServiceID   string    `json:"serviceId"`
	TripID      string    `json:"tripId"`
}

// NewFrequencyFromDB converts a database Frequency row into an API Frequency model.
// serviceDate is the start-of-day in the agency's local timezone.
// The DB stores start_time / end_time as nanoseconds since midnight (time.Duration).
// The resulting StartTime/EndTime are Unix epoch milliseconds.
func NewFrequencyFromDB(dbFreq gtfsdb.Frequency, serviceDate time.Time) Frequency {
	// Correctly compute start of day in the agency's local timezone
	startOfDay := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, serviceDate.Location())

	return Frequency{
		StartTime:  NewModelTime(startOfDay.Add(time.Duration(dbFreq.StartTime))),
		EndTime:    NewModelTime(startOfDay.Add(time.Duration(dbFreq.EndTime))),
		Headway:    NewModelDuration(time.Duration(dbFreq.HeadwaySecs) * time.Second),
		ExactTimes: int(dbFreq.ExactTimes),
	}
}
