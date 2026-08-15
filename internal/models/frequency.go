package models

import (
	"time"

	"maglev.onebusaway.org/gtfsdb"
)

// FrequencyWindow holds the fields common to both legacy frequency shapes.
type FrequencyWindow struct {
	StartTime ModelTime     `json:"startTime"`
	EndTime   ModelTime     `json:"endTime"`
	Headway   ModelDuration `json:"headway"`
}

// Frequency is the generic frequency descriptor embedded in trip-details,
// TripStatus, ArrivalAndDeparture, trips-for-route, trips-for-location,
// and the Schedule sub-object. It matches the legacy FrequencyV2Bean.
type Frequency struct {
	FrequencyWindow
	ExactTimes int `json:"exactTimes"`
}

// ScheduleFrequency is a schedule-for-stop scheduleFrequencies[] entry.
// It matches the legacy ScheduleFrequencyInstanceV2Bean.
type ScheduleFrequency struct {
	FrequencyWindow
	ServiceDate      ModelTime `json:"serviceDate"`
	ServiceID        string    `json:"serviceId"`
	TripID           string    `json:"tripId"`
	StopHeadsign     string    `json:"stopHeadsign,omitempty"`
	ArrivalEnabled   bool      `json:"arrivalEnabled"`
	DepartureEnabled bool      `json:"departureEnabled"`
}

// NewFrequencyFromDB converts a database Frequency row into an API Frequency model.
// serviceDate is the start-of-day in the agency's local timezone.
// The DB stores start_time / end_time as nanoseconds since midnight (time.Duration).
// The resulting StartTime/EndTime are Unix epoch milliseconds.
func NewFrequencyFromDB(dbFreq gtfsdb.Frequency, serviceDate time.Time) Frequency {
	// Correctly compute start of day in the agency's local timezone
	startOfDay := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, serviceDate.Location())

	return Frequency{
		FrequencyWindow: FrequencyWindow{
			StartTime: NewModelTime(startOfDay.Add(time.Duration(dbFreq.StartTime))),
			EndTime:   NewModelTime(startOfDay.Add(time.Duration(dbFreq.EndTime))),
			Headway:   NewModelDuration(time.Duration(dbFreq.HeadwaySecs) * time.Second),
		},
		ExactTimes: int(dbFreq.ExactTimes),
	}
}

// NewScheduleFrequencyFromDB converts a database Frequency row into a
// ScheduleFrequency for use in schedule-for-stop responses.
// serviceDate is the start-of-day in the agency's local timezone.
// serviceID and tripID must already be combined (agencyID_rawID) form.
func NewScheduleFrequencyFromDB(
	dbFreq gtfsdb.Frequency,
	serviceDate time.Time,
	serviceID, tripID, stopHeadsign string,
	arrivalEnabled, departureEnabled bool,
) ScheduleFrequency {
	startOfDay := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(),
		0, 0, 0, 0, serviceDate.Location())

	return ScheduleFrequency{
		FrequencyWindow: FrequencyWindow{
			StartTime: NewModelTime(startOfDay.Add(time.Duration(dbFreq.StartTime))),
			EndTime:   NewModelTime(startOfDay.Add(time.Duration(dbFreq.EndTime))),
			Headway:   NewModelDuration(time.Duration(dbFreq.HeadwaySecs) * time.Second),
		},
		ServiceDate:      NewModelTime(startOfDay),
		ServiceID:        serviceID,
		TripID:           tripID,
		StopHeadsign:     stopHeadsign,
		ArrivalEnabled:   arrivalEnabled,
		DepartureEnabled: departureEnabled,
	}
}
