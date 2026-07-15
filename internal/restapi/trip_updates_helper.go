package restapi

import (
	"cmp"
	"context"
	"log/slog"
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
	dev, ok, discard := pickTripLevelDeviation(tripUpdates)
	if discard {
		// Java's blockNotActive guard fired: the entire VehicleLocationRecord
		// would be discarded. We must NOT fall through to STU/per-stop paths —
		// that would silently recover RT data Java treats as poisoned.
		return 0, false
	}
	if ok {
		return dev, true
	}
	if dev, ok := api.pickClosestSTUDeviation(ctx, tripUpdates, serviceDate, currentTime); ok {
		if exceedsBlockNotActiveThreshold(dev) {
			return 0, false
		}
		return dev, true
	}
	if dev, ok := pickFirstAvailableSTUDelay(tripUpdates); ok {
		if exceedsBlockNotActiveThreshold(dev) {
			return 0, false
		}
		return dev, true
	}
	return 0, false
}

// blockNotActiveThresholdSeconds mirrors Java's blockNotActive filter
// (GtfsRealtimeSource.java:811-821): a schedule deviation whose absolute
// value exceeds 1h causes the entire VehicleLocationRecord to be discarded.
// Every consumer that derives a schedule deviation or a prediction offset
// from a TripUpdate must gate on this same threshold — otherwise the two
// halves of a response (tripStatus.scheduleDeviation from BuildTripStatus
// and predictedArrivalTime from getPredictedTimes) diverge and contradict
// each other when a real-time feed reports an outlier delay.
const blockNotActiveThresholdSeconds = 60 * 60

func exceedsBlockNotActiveThreshold(deviationSeconds int) bool {
	return deviationSeconds > blockNotActiveThresholdSeconds ||
		deviationSeconds < -blockNotActiveThresholdSeconds
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
// trip-level delay across the block wins. Return values:
//
//   - (deviation, true,  false): valid trip-level delay, use it.
//   - (0,         false, true):  Java's blockNotActive guard fired
//     (|delay| > 1h). Caller must DISCARD — do not fall through to STU/per-stop
//     paths, since Java drops the entire VehicleLocationRecord in this case.
//   - (0,         false, false): no trip-level delay was seen; caller may try
//     other strategies (closest-in-time STU, reverse-walk fallback).
func pickTripLevelDeviation(tripUpdates []tripUpdateForTrip) (int, bool, bool) {
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
		return 0, false, false
	}
	if exceedsBlockNotActiveThreshold(deviation) {
		return 0, false, true
	}
	return deviation, true, false
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
// against a stop_id's entries. Mirrors Java's getBlockStopTimeForStopTimeUpdate
// (GtfsRealtimeTripLibrary.java:1125-1188):
//
//  1. If the STU has stop_sequence, prefer the entry with that sequence.
//  2. Otherwise (loop trips, STU without sequence), pick the occurrence whose
//     scheduled arr/dep is closest to stuRefSeconds — Java does
//     Min<>(|stopTime.arrivalTime − time|, |stopTime.departureTime − time|).
//  3. stuRefSeconds<=0 falls back to the first occurrence.
func matchScheduleEntry(entries []schedEntry, stuSeq *uint32, stuRefSeconds int64) (schedArr, schedDep int64) {
	if len(entries) == 0 {
		return 0, 0
	}
	if stuSeq != nil {
		for _, e := range entries {
			if e.seq == int64(*stuSeq) {
				return e.arr, e.dep
			}
		}
	}
	if len(entries) == 1 || stuRefSeconds <= 0 {
		return entries[0].arr, entries[0].dep
	}
	picked := entries[0]
	bestDelta := minAbs(picked.arr-stuRefSeconds, picked.dep-stuRefSeconds)
	for _, e := range entries[1:] {
		d := minAbs(e.arr-stuRefSeconds, e.dep-stuRefSeconds)
		if d < bestDelta {
			bestDelta = d
			picked = e
		}
	}
	return picked.arr, picked.dep
}

// stuReferenceTime returns the STU's approximate target time in seconds
// since service-date midnight, used to disambiguate loop-trip stop_id
// matches when no stop_sequence is provided. Mirrors Java's
// getTimeForStopTimeUpdate (GtfsRealtimeTripLibrary.java:1197-1224).
func stuReferenceTime(stu gtfs.StopTimeUpdate, serviceDate, currentTime time.Time) int64 {
	if stu.Arrival != nil {
		if stu.Arrival.Time != nil {
			return stu.Arrival.Time.Unix() - serviceDate.Unix()
		}
		if stu.Arrival.Delay != nil {
			return int64(currentTime.Sub(serviceDate).Seconds()) - int64(stu.Arrival.Delay.Seconds())
		}
	}
	if stu.Departure != nil {
		if stu.Departure.Time != nil {
			return stu.Departure.Time.Unix() - serviceDate.Unix()
		}
		if stu.Departure.Delay != nil {
			return int64(currentTime.Sub(serviceDate).Seconds()) - int64(stu.Departure.Delay.Seconds())
		}
	}
	return -1
}

func minAbs(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	if a < b {
		return a
	}
	return b
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
	picker := newSTUDeviationPicker(utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate))

	for _, t := range tripUpdates {
		schedMap := loadScheduled(t.tripID)
		for _, stu := range t.tu.StopTimeUpdates {
			var schedArr, schedDep int64
			if stu.StopID != nil {
				refTime := stuReferenceTime(stu, serviceDate, currentTime)
				schedArr, schedDep = matchScheduleEntry(schedMap[*stu.StopID], stu.StopSequence, refTime)
			}
			picker.consider(schedArr, stuPredictedFromArrival(stu, schedArr, serviceDate))
			picker.consider(schedDep, stuPredictedFromDeparture(stu, schedDep, serviceDate))
		}
	}
	if !picker.found {
		return 0, false
	}
	return picker.bestDeviation, true
}

type stuDeviationPicker struct {
	currentSeconds int64
	bestDelta      int64
	bestDeviation  int
	bestIsInPast   bool
	found          bool
}

func newSTUDeviationPicker(currentSeconds int64) stuDeviationPicker {
	return stuDeviationPicker{
		currentSeconds: currentSeconds,
		bestDelta:      int64(math.MaxInt64),
		bestIsInPast:   true,
	}
}

func (p *stuDeviationPicker) consider(scheduledSec, predictedSec int64) {
	if scheduledSec <= 0 || predictedSec <= 0 {
		return
	}
	delta := absInt64(predictedSec - p.currentSeconds)
	isInPast := predictedSec < p.currentSeconds
	if p.shouldReplace(delta, isInPast) {
		p.bestDelta = delta
		p.bestDeviation = int(predictedSec - scheduledSec)
		p.bestIsInPast = isInPast
		p.found = true
	}
}

func (p *stuDeviationPicker) shouldReplace(delta int64, isInPast bool) bool {
	return delta < p.bestDelta || (!isInPast && p.bestIsInPast)
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// pickFirstAvailableSTUDelay is the final fallback when schedule lookup
// failed (missing static stop_times, real-time-only trip IDs, sparse feeds)
// and Java's closest-in-time picker cannot run.
//
// Java's updateBestScheduleDeviation picks the STU whose predicted stop-time
// is nearest to now (GtfsRealtimeTripLibrary.java:1256-1272). We can't do
// that here without schedule times, so we approximate: return the delay of
// the FIRST STU with one, in block-iteration order.
//
// Rationale: real-world feeds publish delays in stop-sequence order and
// typically only for upcoming stops, so the first STU is the freshest
// signal for "the bus's current deviation." Terminal STUs are the worst
// choice — schedules bake recovery time into the last leg, so end-of-line
// delays decay toward 0 even for a bus that is currently very late.
func pickFirstAvailableSTUDelay(tripUpdates []tripUpdateForTrip) (int, bool) {
	for _, t := range tripUpdates {
		for _, stu := range t.tu.StopTimeUpdates {
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
		if err != nil {
			// A real DB error (vs. sql.ErrNoRows / empty) on a partial sort
			// would silently scramble block-trip order — and pickTripLevelDeviation
			// depends on that order ("last delay wins"). Warn and bail.
			warnIfRealDBError(err, "blockTripIDsSortedByStartTime: GetStopTimesForTrip failed, returning input order",
				slog.String("trip_id", id))
			return tripIDs
		}
		if len(sts) == 0 {
			// Trip has no stop_times — skip it from the sort but keep going;
			// it can't contribute a meaningful start time anyway.
			continue
		}
		withStart = append(withStart, tripWithStart{id: id, start: sts[0].ArrivalTime})
	}
	if len(withStart) < len(tripIDs) {
		// Some trips had no stop_times. Returning a partial sort would drop
		// those IDs from the block; keep input order to preserve them.
		return tripIDs
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
