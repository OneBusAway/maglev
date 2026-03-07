package models

import (
	"time"

	"maglev.onebusaway.org/gtfsdb"
)

// Frequency represents a GTFS frequency entry in API responses.
type Frequency struct {
	StartTime int64 `json:"startTime"`
	EndTime   int64 `json:"endTime"`
	// Headway is the time between departures in seconds
	Headway int `json:"headway"`
	// ExactTimes is used internally for business logic but omitted from API responses
	ExactTimes int `json:"-"`
}

// NewFrequencyFromDB converts a database Frequency row into an API Frequency model.
// serviceDate is the start-of-day in the agency's local timezone.
// The DB stores start_time / end_time as nanoseconds since midnight (time.Duration).
// The resulting StartTime/EndTime are Unix epoch milliseconds.
func NewFrequencyFromDB(dbFreq gtfsdb.Frequency, serviceDate time.Time) Frequency {
	// Correctly compute start of day in the agency's local timezone
	startOfDay := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, serviceDate.Location())

	return Frequency{
		StartTime:  startOfDay.Add(time.Duration(dbFreq.StartTime)).UnixMilli(),
		EndTime:    startOfDay.Add(time.Duration(dbFreq.EndTime)).UnixMilli(),
		Headway:    int(dbFreq.HeadwaySecs),
		ExactTimes: int(dbFreq.ExactTimes),
	}
}

// NewFrequency creates a Frequency with explicit values (times already in epoch ms).
func NewFrequency(startTime, endTime int64, headway, exactTimes int) Frequency {
	return Frequency{
		StartTime:  startTime,
		EndTime:    endTime,
		Headway:    headway,
		ExactTimes: exactTimes,
	}
}
