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
	tripUpdates := api.collectBlockTripUpdates(tripIDs)
	if len(tripUpdates) == 0 {
		return 0, false
	}
	if dev, ok := pickTripLevelDeviation(tripUpdates); ok {
		return dev, true
	}
	if dev, ok := api.pickClosestSTUDeviation(ctx, tripUpdates, serviceDate, currentTime); ok {
		return dev, true
	}
	return pickReverseWalkSTUDelay(tripUpdates)
}

type tripUpdateForTrip struct {
	tripID string
	tu     gtfs.Trip
}

// collectBlockTripUpdates flattens every TripUpdate matching any tripID
// into a slice in the order they appear, preserving block-trip-start order
// when the caller passes a sorted slice.
func (api *RestAPI) collectBlockTripUpdates(tripIDs []string) []tripUpdateForTrip {
	var out []tripUpdateForTrip
	for _, id := range tripIDs {
		for _, tu := range api.GtfsManager.GetTripUpdatesForTrip(id) {
			out = append(out, tripUpdateForTrip{tripID: id, tu: tu})
		}
	}
	return out
}

// pickTripLevelDeviation implements Java's unconditional overwrite: LAST
// trip-level delay across the block wins. Returns (0, false) when no
// trip-level delay was set OR when |delay|>1h (Java's blockNotActive guard).
func pickTripLevelDeviation(tripUpdates []tripUpdateForTrip) (int, bool) {
	var (
		deviation int
		found     bool
	)
	for _, t := range tripUpdates {
		if t.tu.Delay != nil {
			deviation = int(t.tu.Delay.Seconds())
			found = true
		}
	}
	if !found {
		return 0, false
	}
	const javaBlockNotActiveThreshold = 60 * 60
	if deviation > javaBlockNotActiveThreshold || deviation < -javaBlockNotActiveThreshold {
		return 0, false
	}
	return deviation, true
}

// schedEntry captures one (stop_sequence, arrival, departure) tuple for a
// stop_id; loop/lasso trips need multiple entries per stop_id.
type schedEntry struct{ seq, arr, dep int64 }

// loadScheduledForTrip returns a lazy loader that caches per-trip scheduled
// stop-time entries keyed by stop_id (with multiple entries per stop_id for
// loop trips). Closure captures the cache map for the lifetime of one
// pickClosestSTUDeviation call.
func (api *RestAPI) loadScheduledForTrip(ctx context.Context) func(string) map[string][]schedEntry {
	cache := map[string]map[string][]schedEntry{}
	return func(tripID string) map[string][]schedEntry {
		if s, ok := cache[tripID]; ok {
			return s
		}
		s := map[string][]schedEntry{}
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		if err == nil {
			for _, st := range stopTimes {
				s[st.StopID] = append(s[st.StopID], schedEntry{
					seq: st.StopSequence,
					arr: st.ArrivalTime / int64(time.Second),
					dep: st.DepartureTime / int64(time.Second),
				})
			}
		}
		cache[tripID] = s
		return s
	}
}

// matchScheduleEntry resolves the right scheduled (arr, dep) for an STU
// against a stop_id's entries. Loop trips: prefer the entry matching the
// STU's stop_sequence; fall back to the first occurrence.
func matchScheduleEntry(entries []schedEntry, stuSeq *uint32) (schedArr, schedDep int64) {
	if len(entries) == 0 {
		return 0, 0
	}
	picked := entries[0]
	if stuSeq != nil {
		for _, e := range entries {
			if e.seq == int64(*stuSeq) {
				picked = e
				break
			}
		}
	}
	return picked.arr, picked.dep
}

// stuPredictedFromArrival computes the predicted arrival-seconds-since-midnight
// for an STU. Returns 0 when nothing useful is set.
//
// TODO(dst): Time path uses UTC epoch math, which can be off by 1h for
// trips that span the DST transition. Rare in practice.
func stuPredictedFromArrival(stu gtfs.StopTimeUpdate, schedArr int64, serviceDate time.Time) int64 {
	if stu.Arrival == nil || schedArr <= 0 {
		return 0
	}
	switch {
	case stu.Arrival.Time != nil:
		return stu.Arrival.Time.Unix() - serviceDate.Unix()
	case stu.Arrival.Delay != nil:
		return schedArr + int64(stu.Arrival.Delay.Seconds())
	}
	return 0
}

// stuPredictedFromDeparture mirrors stuPredictedFromArrival for departure events.
func stuPredictedFromDeparture(stu gtfs.StopTimeUpdate, schedDep int64, serviceDate time.Time) int64 {
	if stu.Departure == nil || schedDep <= 0 {
		return 0
	}
	switch {
	case stu.Departure.Time != nil:
		return stu.Departure.Time.Unix() - serviceDate.Unix()
	case stu.Departure.Delay != nil:
		return schedDep + int64(stu.Departure.Delay.Seconds())
	}
	return 0
}

// pickClosestSTUDeviation implements Java's updateBestScheduleDeviation:
// across every STU in every block trip's TripUpdate, pick the one whose
// predicted stop-time is closest to currentTime; tiebreak prefers stops
// still in the future.
func (api *RestAPI) pickClosestSTUDeviation(ctx context.Context, tripUpdates []tripUpdateForTrip, serviceDate, currentTime time.Time) (int, bool) {
	loadScheduled := api.loadScheduledForTrip(ctx)
	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	bestDelta := int64(math.MaxInt64)
	bestDeviation := 0
	bestIsInPast := true
	found := false
	consider := func(scheduledSec, predictedSec int64) {
		if scheduledSec <= 0 || predictedSec <= 0 {
			return
		}
		delta := predictedSec - currentSeconds
		if delta < 0 {
			delta = -delta
		}
		isInPast := predictedSec < currentSeconds
		if delta < bestDelta || (!isInPast && bestIsInPast) {
			bestDelta = delta
			bestDeviation = int(predictedSec - scheduledSec)
			bestIsInPast = isInPast
			found = true
		}
	}

	for _, t := range tripUpdates {
		schedMap := loadScheduled(t.tripID)
		for _, stu := range t.tu.StopTimeUpdates {
			var schedArr, schedDep int64
			if stu.StopID != nil {
				schedArr, schedDep = matchScheduleEntry(schedMap[*stu.StopID], stu.StopSequence)
			}
			consider(schedArr, stuPredictedFromArrival(stu, schedArr, serviceDate))
			consider(schedDep, stuPredictedFromDeparture(stu, schedDep, serviceDate))
		}
	}
	if !found {
		return 0, false
	}
	return bestDeviation, true
}

// pickReverseWalkSTUDelay is the final fallback when schedule lookup failed
// (e.g. test fixtures without static stop times) but the feed still carries
// per-stop delays. Returns the latest stop's delay in block-iteration order.
func pickReverseWalkSTUDelay(tripUpdates []tripUpdateForTrip) (int, bool) {
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
