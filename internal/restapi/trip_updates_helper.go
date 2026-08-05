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

// StopDelayInfo represents the arrival and departure delays (in seconds,
// positive = late) reported by a GTFS-RT TripUpdate for a single stop-time.
// A value of 0 means the schedule is being met exactly; nothing here
// distinguishes "no update" from "on-time" — callers gate on presence of the
// enclosing map entry.
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

// loadScheduledForTrips batch-loads every trip's scheduled stop-time entries
// (keyed by stop_id, with multiple entries per stop_id for loop trips) via
// one GetStopTimesForTripIDs call instead of one GetStopTimesForTrip per
// trip in a lazy loader loop. Missing trips get an empty map so callers
// don't need to nil-check.
func (api *RestAPI) loadScheduledForTrips(ctx context.Context, tripIDs []string) map[string]map[string][]schedEntry {
	out := make(map[string]map[string][]schedEntry, len(tripIDs))
	for _, id := range tripIDs {
		out[id] = map[string][]schedEntry{}
	}
	if len(tripIDs) == 0 {
		return out
	}
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, tripIDs)
	if err != nil {
		return out
	}
	for _, st := range stopTimes {
		m := out[st.TripID]
		m[st.StopID] = append(m[st.StopID], schedEntry{
			seq: st.StopSequence,
			arr: st.ArrivalTime / int64(time.Second),
			dep: st.DepartureTime / int64(time.Second),
		})
	}
	return out
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
//
// All arms use utils.CalculateSecondsSinceServiceDate so wall-clock semantics
// (not raw Unix-epoch subtraction) drive the result, keeping post-midnight
// stop times and cross-DST spans consistent.
func stuReferenceTime(stu gtfs.StopTimeUpdate, serviceDate, currentTime time.Time) int64 {
	if stu.Arrival != nil {
		if stu.Arrival.Time != nil {
			return utils.CalculateSecondsSinceServiceDate(*stu.Arrival.Time, serviceDate)
		}
		if stu.Arrival.Delay != nil {
			return utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate) - int64(stu.Arrival.Delay.Seconds())
		}
	}
	if stu.Departure != nil {
		if stu.Departure.Time != nil {
			return utils.CalculateSecondsSinceServiceDate(*stu.Departure.Time, serviceDate)
		}
		if stu.Departure.Delay != nil {
			return utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate) - int64(stu.Departure.Delay.Seconds())
		}
	}
	return -1
}

func minAbs(a, b int64) int64 {
	a, b = absInt64(a), absInt64(b)
	if a < b {
		return a
	}
	return b
}

// stuPredictedFromEvent computes the predicted seconds-since-midnight for a
// single GTFS-RT StopTimeEvent (arrival or departure -- they share the same
// struct shape). Returns 0 when nothing useful is set. Uses utils.CalculateSeconds
// SinceServiceDate for the Time path so wall-clock semantics stay correct
// across DST transitions and post-midnight stop times.
func stuPredictedFromEvent(event *gtfs.StopTimeEvent, sched int64, serviceDate time.Time) int64 {
	if event == nil || sched <= 0 {
		return 0
	}
	switch {
	case event.Time != nil:
		return utils.CalculateSecondsSinceServiceDate(*event.Time, serviceDate)
	case event.Delay != nil:
		return sched + int64(event.Delay.Seconds())
	}
	return 0
}

// pickClosestSTUDeviation implements Java's updateBestScheduleDeviation:
// across every STU in every block trip's TripUpdate, pick the one whose
// predicted stop-time is closest to currentTime; tiebreak prefers stops
// still in the future.
func (api *RestAPI) pickClosestSTUDeviation(ctx context.Context, tripUpdates []tripUpdateForTrip, serviceDate, currentTime time.Time) (int, bool) {
	tripIDs := make([]string, 0, len(tripUpdates))
	seen := make(map[string]struct{}, len(tripUpdates))
	for _, t := range tripUpdates {
		if _, ok := seen[t.tripID]; ok {
			continue
		}
		seen[t.tripID] = struct{}{}
		tripIDs = append(tripIDs, t.tripID)
	}
	scheduled := api.loadScheduledForTrips(ctx, tripIDs)
	picker := newSTUDeviationPicker(utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate))

	for _, t := range tripUpdates {
		schedMap := scheduled[t.tripID]
		for _, stu := range t.tu.StopTimeUpdates {
			var schedArr, schedDep int64
			if stu.StopID != nil {
				refTime := stuReferenceTime(stu, serviceDate, currentTime)
				schedArr, schedDep = matchScheduleEntry(schedMap[*stu.StopID], stu.StopSequence, refTime)
			}
			picker.consider(schedArr, stuPredictedFromEvent(stu.Arrival, schedArr, serviceDate))
			picker.consider(schedDep, stuPredictedFromEvent(stu.Departure, schedDep, serviceDate))
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

// shouldReplace decides whether a candidate STU displaces the incumbent.
// A strictly-closer candidate always wins. When deltas are equal, a future
// candidate is preferred over a past incumbent — Java's tiebreak favors
// upcoming stops. Future candidates whose delta exceeds the incumbent's
// must NOT bypass the delta comparison, so future-preference is gated on
// `delta <= p.bestDelta`.
func (p *stuDeviationPicker) shouldReplace(delta int64, isInPast bool) bool {
	if delta < p.bestDelta {
		return true
	}
	return !isInPast && p.bestIsInPast && delta <= p.bestDelta
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
// applyTripUpdatesToRecord. Uses the cached trips.min_arrival_time column
// (populated at import time — see gtfsdb/schema.sql:294) via a single
// batched query, so this is one DB round-trip regardless of block size
// instead of N GetStopTimesForTrip calls. Falls back to input order on
// DB error or when any trip is missing a cached start time.
func (api *RestAPI) blockTripIDsSortedByStartTime(ctx context.Context, tripIDs []string) []string {
	if len(tripIDs) <= 1 {
		return tripIDs
	}
	rows, err := api.GtfsManager.GtfsDB.Queries.GetTripTimeBoundsByIDs(ctx, tripIDs)
	if err != nil {
		// A real DB error on a partial sort would silently scramble
		// block-trip order — pickTripLevelDeviation depends on that order
		// ("last delay wins"). Warn and bail to input order.
		warnIfRealDBError(err, "blockTripIDsSortedByStartTime: GetTripTimeBoundsByIDs failed, returning input order",
			slog.Int("trip_count", len(tripIDs)))
		return tripIDs
	}
	startByID := make(map[string]int64, len(rows))
	for _, r := range rows {
		if !r.MinArrivalTime.Valid {
			continue
		}
		startByID[r.ID] = r.MinArrivalTime.Int64
	}
	// Any trip missing a cached min_arrival_time -- a partial sort would
	// order the others but leave the missing IDs at their default 0 key,
	// silently promoting them to "earliest" and scrambling the deviation
	// picker's "last delay wins" contract. Per-ID membership check
	// (instead of len(startByID) < len(tripIDs)) makes intent explicit and
	// is robust to any future map-population change.
	for _, id := range tripIDs {
		if _, ok := startByID[id]; !ok {
			return tripIDs
		}
	}
	out := make([]string, len(tripIDs))
	copy(out, tripIDs)
	slices.SortFunc(out, func(a, b string) int { return cmp.Compare(startByID[a], startByID[b]) })
	return out
}

// blockShiftTripIDsSortedByStartTime resolves targetTripID's block trips for
// serviceDate, applies the same keepShiftContainingTrip split that
// computeScheduledBlockSnapshot uses, and returns the shift's trip IDs in
// start-time order. Callers that pass the raw block-trip list to
// GetScheduleDeviationForBlock end up with an evening-bus delay landing on
// a morning arrival when a feed reuses one block_id across an entire day.
// The shift filter matches the snapshot's own filtering so the deviation
// picker sees the same trips the block snapshot does.
//
// Uses the cached trips.min_arrival_time / max_departure_time columns via a
// single batched query — no per-trip stop_times fetch.
func (api *RestAPI) blockShiftTripIDsSortedByStartTime(ctx context.Context, targetTripID string, serviceDate time.Time) []string {
	rawTripIDs := api.blockTripIDsForServiceDate(ctx, targetTripID, serviceDate)
	if len(rawTripIDs) <= 1 {
		return rawTripIDs
	}
	rows, err := api.GtfsManager.GtfsDB.Queries.GetTripTimeBoundsByIDs(ctx, rawTripIDs)
	if err != nil {
		warnIfRealDBError(err, "blockShiftTripIDsSortedByStartTime: GetTripTimeBoundsByIDs failed, falling back to unfiltered sort",
			slog.Int("trip_count", len(rawTripIDs)))
		return api.blockTripIDsSortedByStartTime(ctx, rawTripIDs)
	}
	// Build minimal blockTripData rows — keepShiftContainingTrip needs only
	// id, firstSeconds, lastSeconds. Any trip missing a cached bound is
	// skipped from the shift resolution but retained in the output so the
	// deviation picker still considers it (matches
	// blockTripIDsSortedByStartTime's degraded-mode behaviour).
	minimal := make([]blockTripData, 0, len(rows))
	for _, r := range rows {
		if !r.MinArrivalTime.Valid || !r.MaxDepartureTime.Valid {
			continue
		}
		minimal = append(minimal, blockTripData{
			id:           r.ID,
			firstSeconds: r.MinArrivalTime.Int64 / int64(time.Second),
			lastSeconds:  r.MaxDepartureTime.Int64 / int64(time.Second),
		})
	}
	// Missing a bound anywhere → the shift split would exclude those trips
	// silently. Per-ID membership check (instead of len(minimal) <
	// len(rawTripIDs)) makes intent explicit and stays correct if
	// GetTripTimeBoundsByIDs ever returns extra or duplicate rows.
	presentIDs := make(map[string]struct{}, len(minimal))
	for _, m := range minimal {
		presentIDs[m.id] = struct{}{}
	}
	for _, id := range rawTripIDs {
		if _, ok := presentIDs[id]; !ok {
			return api.blockTripIDsSortedByStartTime(ctx, rawTripIDs)
		}
	}
	slices.SortFunc(minimal, func(a, b blockTripData) int {
		return cmp.Compare(a.firstSeconds, b.firstSeconds)
	})
	shift := keepShiftContainingTrip(minimal, targetTripID)
	if len(shift) == 0 {
		// Target isn't in the block-trip list (shouldn't happen — target's
		// own trip drives the lookup — but keepShiftContainingTrip returns
		// nil rather than panic if it slips). Preserve current behaviour.
		return api.blockTripIDsSortedByStartTime(ctx, rawTripIDs)
	}
	out := make([]string, len(shift))
	for i, t := range shift {
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
