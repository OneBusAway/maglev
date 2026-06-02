package restapi

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/utils"
)

type StopDelayInfo struct {
	ArrivalDelay   int64
	DepartureDelay int64
}

// GetScheduleDeviationForBlock returns the schedule deviation in seconds
// (positive = late) across every TripUpdate matching any tripID. Mirrors
// Java's GtfsRealtimeTripLibrary.applyTripUpdatesToRecord (lines 932-1014):
//
//  1. LAST trip-level delay across the block wins (Java overwrites
//     unconditionally; the tripUpdateHasDelay flag then suppresses per-STU
//     processing). Publishers often emit delay=0 for future trips, which
//     overrides the current trip's lateness — that's why Java reports 0.
//  2. If no trip-level delay anywhere, closest-in-time STU across the block.
//  3. Java's blockNotActive guard (GtfsRealtimeSource.java:811-821) discards
//     records with |deviation| > 1h; we return (0, false) instead.
//
// tripIDs MUST be in block-trip-start order; caller sorts via
// blockTripIDsSortedByStartTime.
func (api *RestAPI) GetScheduleDeviationForBlock(ctx context.Context, tripIDs []string, serviceDate time.Time, currentTime time.Time) (int, bool) {
	if len(tripIDs) == 0 {
		return 0, false
	}

	type tripUpdateForTrip struct {
		tripID string
		tu     gtfs.Trip
	}
	var tripUpdates []tripUpdateForTrip
	for _, id := range tripIDs {
		for _, tu := range api.GtfsManager.GetTripUpdatesForTrip(id) {
			tripUpdates = append(tripUpdates, tripUpdateForTrip{tripID: id, tu: tu})
		}
	}
	if len(tripUpdates) == 0 {
		return 0, false
	}

	// (1) Mirror Java's unconditional overwrite: LAST trip-level delay wins.
	//     We do NOT short-circuit on the first delay because Java doesn't —
	//     a later block trip's delay overwrites an earlier one.
	var (
		tripLevelDeviation int
		tripLevelFound     bool
	)
	for _, t := range tripUpdates {
		if t.tu.Delay != nil {
			tripLevelDeviation = int(t.tu.Delay.Seconds())
			tripLevelFound = true
		}
	}
	if tripLevelFound {
		const javaBlockNotActiveThreshold = 60 * 60
		if tripLevelDeviation > javaBlockNotActiveThreshold || tripLevelDeviation < -javaBlockNotActiveThreshold {
			return 0, false
		}
		return tripLevelDeviation, true
	}

	// (2) No trip-level delay anywhere → closest-in-time STU across all block
	//     trips. Java's updateBestScheduleDeviation only fires this path when
	//     tripUpdateHasDelay was never set, so we mirror that.

	// Per-trip scheduled stop-time cache, lazy-loaded.
	scheduledByTrip := make(map[string]map[string]struct{ arr, dep int64 }, len(tripIDs))
	loadScheduled := func(tripID string) map[string]struct{ arr, dep int64 } {
		if s, ok := scheduledByTrip[tripID]; ok {
			return s
		}
		s := map[string]struct{ arr, dep int64 }{}
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		if err == nil {
			for _, st := range stopTimes {
				s[st.StopID] = struct{ arr, dep int64 }{
					arr: st.ArrivalTime / int64(time.Second),
					dep: st.DepartureTime / int64(time.Second),
				}
			}
		}
		scheduledByTrip[tripID] = s
		return s
	}

	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	bestDelta := int64(math.MaxInt64)
	bestDeviation := 0
	bestIsInPast := true
	found := false

	considerCandidate := func(scheduledSec, predictedSec int64) {
		if scheduledSec <= 0 {
			return
		}
		deviation := predictedSec - scheduledSec
		delta := predictedSec - currentSeconds
		if delta < 0 {
			delta = -delta
		}
		isInPast := predictedSec < currentSeconds
		if delta < bestDelta || (!isInPast && bestIsInPast) {
			bestDelta = delta
			bestDeviation = int(deviation)
			bestIsInPast = isInPast
			found = true
		}
	}

	for _, t := range tripUpdates {
		schedMap := loadScheduled(t.tripID)
		for _, stu := range t.tu.StopTimeUpdates {
			var schedArr, schedDep int64
			if stu.StopID != nil {
				if s, ok := schedMap[*stu.StopID]; ok {
					schedArr, schedDep = s.arr, s.dep
				}
			}

			if stu.Arrival != nil && schedArr > 0 {
				var predicted int64
				switch {
				case stu.Arrival.Time != nil:
					predicted = stu.Arrival.Time.Unix() - serviceDate.Unix()
				case stu.Arrival.Delay != nil:
					predicted = schedArr + int64(stu.Arrival.Delay.Seconds())
				}
				if predicted > 0 {
					considerCandidate(schedArr, predicted)
				}
			}
			if stu.Departure != nil && schedDep > 0 {
				var predicted int64
				switch {
				case stu.Departure.Time != nil:
					predicted = stu.Departure.Time.Unix() - serviceDate.Unix()
				case stu.Departure.Delay != nil:
					predicted = schedDep + int64(stu.Departure.Delay.Seconds())
				}
				if predicted > 0 {
					considerCandidate(schedDep, predicted)
				}
			}
		}
	}

	if found {
		return bestDeviation, true
	}

	// Fallback: schedule lookup failed (e.g. test fixtures without static stop
	// times) but the feed carries per-stop delays. Reverse-walk every block
	// trip's updates and return the latest stop's delay.
	for i := len(tripUpdates) - 1; i >= 0; i-- {
		stus := tripUpdates[i].tu.StopTimeUpdates
		for j := len(stus) - 1; j >= 0; j-- {
			stu := stus[j]
			if stu.Arrival != nil && stu.Arrival.Delay != nil {
				return int(stu.Arrival.Delay.Seconds()), true
			}
			if stu.Departure != nil && stu.Departure.Delay != nil {
				return int(stu.Departure.Delay.Seconds()), true
			}
		}
	}
	return 0, false
}

// blockTripIDsSortedByStartTime returns the block's trip IDs sorted by each
// trip's earliest scheduled arrival time — the order Java iterates them in
// applyTripUpdatesToRecord. Falls back to the input order if any trip's
// stop times can't be loaded.
func (api *RestAPI) blockTripIDsSortedByStartTime(ctx context.Context, tripIDs []string) []string {
	if len(tripIDs) <= 1 {
		return tripIDs
	}
	type tripWithStart struct {
		id    string
		start int64
	}
	withStart := make([]tripWithStart, 0, len(tripIDs))
	for _, id := range tripIDs {
		sts, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, id)
		if err != nil || len(sts) == 0 {
			// On lookup failure, preserve input order by returning the
			// original slice — sorting partial data would scramble it.
			return tripIDs
		}
		withStart = append(withStart, tripWithStart{id: id, start: sts[0].ArrivalTime})
	}
	slices.SortFunc(withStart, func(a, b tripWithStart) int { return cmp.Compare(a.start, b.start) })
	out := make([]string, len(withStart))
	for i, t := range withStart {
		out[i] = t.id
	}
	return out
}

// GetStopDelaysFromTripUpdates returns a map of stop ID → per-stop delay information
// (arrival and departure delays in seconds) derived from the GTFS-RT StopTimeUpdates
// for the given trip. Returns an empty map when no real-time data is available.
func (api *RestAPI) GetStopDelaysFromTripUpdates(tripID string) map[string]StopDelayInfo {
	delays := make(map[string]StopDelayInfo)

	tripUpdates := api.GtfsManager.GetTripUpdatesForTrip(tripID)
	if len(tripUpdates) == 0 {
		return delays
	}

	for _, stu := range tripUpdates[0].StopTimeUpdates {
		if stu.StopID == nil {
			continue
		}

		info := StopDelayInfo{}
		if stu.Arrival != nil && stu.Arrival.Delay != nil {
			info.ArrivalDelay = int64(stu.Arrival.Delay.Seconds())
		}
		if stu.Departure != nil && stu.Departure.Delay != nil {
			info.DepartureDelay = int64(stu.Departure.Delay.Seconds())
		}

		delays[*stu.StopID] = info
	}

	return delays
}
