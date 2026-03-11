package models

// ScheduleStopTime represents an individual stop time in a schedule
type ScheduleStopTime struct {
	ArrivalEnabled   bool   `json:"arrivalEnabled"`
	ArrivalTime      int64  `json:"arrivalTime"`
	DepartureEnabled bool   `json:"departureEnabled"`
	DepartureTime    int64  `json:"departureTime"`
	ServiceID        string `json:"serviceId"`
	StopHeadsign     string `json:"stopHeadsign"`
	TripID           string `json:"tripId"`
}

// ScheduleFrequency represents a frequency entry within a stop schedule response.
// Unlike the Frequency model (used in trip-details/arrivals), this struct includes
// serviceId and tripId context for the schedule-for-stop and schedule-for-route endpoints.
type ScheduleFrequency struct {
	ServiceDate int64  `json:"serviceDate"`
	StartTime   int64  `json:"startTime"`
	EndTime     int64  `json:"endTime"`
	Headway     int    `json:"headway"`
	ServiceID   string `json:"serviceId"`
	TripID      string `json:"tripId"`
}

// StopRouteDirectionSchedule represents schedule for a specific direction of a route
type StopRouteDirectionSchedule struct {
	ScheduleFrequencies []ScheduleFrequency `json:"scheduleFrequencies"`
	ScheduleStopTimes   []ScheduleStopTime  `json:"scheduleStopTimes"`
	TripHeadsign        string              `json:"tripHeadsign"`
}

// StopRouteSchedule represents the schedule for a route at a stop
type StopRouteSchedule struct {
	RouteID                     string                       `json:"routeId"`
	StopRouteDirectionSchedules []StopRouteDirectionSchedule `json:"stopRouteDirectionSchedules"`
}

// ScheduleForStopEntry represents the main data entry for schedule-for-stop
type ScheduleForStopEntry struct {
	Date               int64               `json:"date"`
	StopID             string              `json:"stopId"`
	StopRouteSchedules []StopRouteSchedule `json:"stopRouteSchedules"`
}

// NewScheduleStopTime creates a new ScheduleStopTime
func NewScheduleStopTime(arrivalTime, departureTime int64, serviceID, stopHeadsign, tripID string) ScheduleStopTime {
	return ScheduleStopTime{
		ArrivalEnabled:   true,
		ArrivalTime:      arrivalTime,
		DepartureEnabled: true,
		DepartureTime:    departureTime,
		ServiceID:        serviceID,
		StopHeadsign:     stopHeadsign,
		TripID:           tripID,
	}
}

// NewStopRouteDirectionSchedule creates a new StopRouteDirectionSchedule
func NewStopRouteDirectionSchedule(tripHeadsign string, stopTimes []ScheduleStopTime, frequencies []ScheduleFrequency) StopRouteDirectionSchedule {
	if frequencies == nil {
		frequencies = []ScheduleFrequency{}
	}
	return StopRouteDirectionSchedule{
		ScheduleFrequencies: frequencies,
		ScheduleStopTimes:   stopTimes,
		TripHeadsign:        tripHeadsign,
	}
}

// NewStopRouteSchedule creates a new StopRouteSchedule
func NewStopRouteSchedule(routeID string, directionSchedules []StopRouteDirectionSchedule) StopRouteSchedule {
	return StopRouteSchedule{
		RouteID:                     routeID,
		StopRouteDirectionSchedules: directionSchedules,
	}
}

// NewScheduleForStopEntry creates a new ScheduleForStopEntry
func NewScheduleForStopEntry(stopID string, date int64, routeSchedules []StopRouteSchedule) ScheduleForStopEntry {
	return ScheduleForStopEntry{
		Date:               date,
		StopID:             stopID,
		StopRouteSchedules: routeSchedules,
	}
}
