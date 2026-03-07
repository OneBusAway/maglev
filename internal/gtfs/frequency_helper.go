package gtfs

import (
	"context"
	"time"

	"maglev.onebusaway.org/gtfsdb"
)

// IsFrequencyBasedTrip returns true if the given trip has any frequency entries in the database.
func (manager *Manager) IsFrequencyBasedTrip(tripID string) bool {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()

	_, isFreq := manager.frequencyTripIDs[tripID]
	return isFreq
}

// GetFrequenciesForTrip returns all frequency entries for a trip, ordered by start_time.
func (manager *Manager) GetFrequenciesForTrip(ctx context.Context, tripID string) ([]gtfsdb.Frequency, error) {
	return manager.GtfsDB.Queries.GetFrequenciesForTrip(ctx, tripID)
}

// GetFrequenciesForTrips returns all frequency entries for multiple trips in a single batch query.
func (manager *Manager) GetFrequenciesForTrips(ctx context.Context, tripIDs []string) ([]gtfsdb.Frequency, error) {
	if len(tripIDs) == 0 {
		return nil, nil
	}
	return manager.GtfsDB.Queries.GetFrequenciesForTrips(ctx, tripIDs)
}

// GetActiveHeadway returns the frequency entry that is active at the given time,
// or nil if no frequency window covers the time.
// currentTimeNanos is nanoseconds since midnight (same unit as DB start_time/end_time).
func GetActiveHeadway(frequencies []gtfsdb.Frequency, currentTimeNanos int64) *gtfsdb.Frequency {
	for i := range frequencies {
		if currentTimeNanos >= frequencies[i].StartTime && currentTimeNanos < frequencies[i].EndTime {
			return &frequencies[i]
		}
	}
	return nil
}

// GetActiveHeadwayForTime returns the frequency entry active at a specific wall-clock time,
func GetActiveHeadwayForTime(frequencies []gtfsdb.Frequency, serviceDate time.Time, now time.Time) *gtfsdb.Frequency {
	startOfDay := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, serviceDate.Location())
	nanosSinceMidnight := now.Sub(startOfDay).Nanoseconds()
	return GetActiveHeadway(frequencies, nanosSinceMidnight)
}

// ExpandFrequencyTrips generates the departure times for a schedule-based frequency
// (exact_times = 1). For each frequency window, it creates repeated stop-time entries
// offset by multiples of the headway from the window start time.
//
// Parameters:
//   - baseStopTimes: the template stop times for the trip (from stop_times table)
//   - freq: a single frequency entry with exact_times = 1
//
// Returns expanded stop times with adjusted arrival/departure times.
func ExpandFrequencyTrips(baseStopTimes []gtfsdb.StopTime, freq gtfsdb.Frequency) []gtfsdb.StopTime {
	if len(baseStopTimes) == 0 {
		return nil
	}

	headwayNanos := freq.HeadwaySecs * int64(time.Second)
	if headwayNanos <= 0 {
		return nil
	}

	// The base offset is the first stop's arrival time in the template
	baseOffset := baseStopTimes[0].ArrivalTime

	numDepartures := int((freq.EndTime - freq.StartTime) / headwayNanos)
	expanded := make([]gtfsdb.StopTime, 0, numDepartures*len(baseStopTimes))

	for departureBase := freq.StartTime; departureBase < freq.EndTime; departureBase += headwayNanos {
		// The time shift is: (departureBase - baseOffset)
		// This shifts all stop times so the first stop departs at departureBase
		shift := departureBase - baseOffset

		for _, st := range baseStopTimes {
			// Create a full struct copy, then modify only the time fields
			expandedST := st
			expandedST.ArrivalTime += shift
			expandedST.DepartureTime += shift

			expanded = append(expanded, expandedST)
		}
	}

	return expanded
}

// GroupFrequenciesByTrip groups a flat slice of frequency rows (from a batch query)
// into a map keyed by trip ID.
func GroupFrequenciesByTrip(frequencies []gtfsdb.Frequency) map[string][]gtfsdb.Frequency {
	result := make(map[string][]gtfsdb.Frequency)
	for _, f := range frequencies {
		result[f.TripID] = append(result[f.TripID], f)
	}
	return result
}
