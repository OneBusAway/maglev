package models

import (
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/servicedate"
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
// serviceDate identifies the service day (local midnight, or any instant on that day).
// Offsets are added to the service-day start, noon minus twelve hours, not local midnight.
// The DB stores start_time / end_time as nanoseconds since that start (time.Duration).
// The resulting StartTime/EndTime are Unix epoch milliseconds.
func NewFrequencyFromDB(dbFreq gtfsdb.Frequency, serviceDate time.Time) Frequency {
	return NewFrequencyFromServiceStart(dbFreq, servicedate.OffsetBase(serviceDate))
}

// NewFrequencyFromServiceStart converts a Frequency row, adding its offsets to serviceStart.
func NewFrequencyFromServiceStart(dbFreq gtfsdb.Frequency, serviceStart time.Time) Frequency {
	return Frequency{
		FrequencyWindow: frequencyWindowFrom(dbFreq, serviceStart),
		ExactTimes:      int(dbFreq.ExactTimes),
	}
}

// NewFrequencyWindowFromDB converts a database Frequency row's window into absolute times,
// for the frequency shapes that carry a window but no exactTimes field. See
// NewFrequencyFromDB for the unit conventions involved.
func NewFrequencyWindowFromDB(dbFreq gtfsdb.Frequency, serviceDate time.Time) FrequencyWindow {
	return frequencyWindowFrom(dbFreq, servicedate.OffsetBase(serviceDate))
}

func frequencyWindowFrom(dbFreq gtfsdb.Frequency, startOfDay time.Time) FrequencyWindow {
	return FrequencyWindow{
		StartTime: NewModelTime(startOfDay.Add(time.Duration(dbFreq.StartTime))),
		EndTime:   NewModelTime(startOfDay.Add(time.Duration(dbFreq.EndTime))),
		Headway:   NewModelDuration(time.Duration(dbFreq.HeadwaySecs) * time.Second),
	}
}

// NewScheduleFrequencyFromDB converts a database Frequency row into a
// ScheduleFrequency for use in schedule-for-stop responses.
// serviceDate identifies the service day. Offsets are added to its service-day start.
// serviceID and tripID must already be combined (agencyID_rawID) form.
func NewScheduleFrequencyFromDB(
	dbFreq gtfsdb.Frequency,
	serviceDate time.Time,
	serviceID, tripID, stopHeadsign string,
	arrivalEnabled, departureEnabled bool,
) ScheduleFrequency {
	serviceStart := servicedate.OffsetBase(serviceDate)

	return ScheduleFrequency{
		FrequencyWindow:  frequencyWindowFrom(dbFreq, serviceStart),
		ServiceDate:      NewModelTime(serviceStart),
		ServiceID:        serviceID,
		TripID:           tripID,
		StopHeadsign:     stopHeadsign,
		ArrivalEnabled:   arrivalEnabled,
		DepartureEnabled: departureEnabled,
	}
}
