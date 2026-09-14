package restapi

import (
	"cmp"
	"context"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// blockStopMetric is one stop on the block's timeline with cumulative block
// distance / block sequence (both 0-indexed across the block in trip-start
// order).
type blockStopMetric struct {
	TripID               string
	StopID               string
	StopSequenceInTrip   int
	BlockSequence        int
	EffectiveStopSeconds int64 // wall-clock seconds since service-date midnight
	DistanceAlongBlock   float64
	DistanceAlongTrip    float64
}

// scheduledBlockSnapshot is a block's interpolated state at one currentTime —
// the no-real-time-vehicle equivalent of Java's BlockLocation.
type scheduledBlockSnapshot struct {
	Stops         []blockStopMetric
	StopIndex     map[scheduledStopKey]int
	NextStopIndex int // -1 when currentTime is past the block's last stop

	// Block-level interpolated distance at currentTime.
	DistanceAlongBlock float64

	// InRange is true when currentTime falls within the shift's [firstStop,
	// lastStop] scheduled span. When false, DistanceAlongBlock is clamped to
	// a block boundary and downstream code must NOT treat the derived
	// per-trip fields (ActiveTripScheduledDistance, distanceFromStop) as
	// meaningful. Mirrors Java's null-BlockLocation semantics
	// (ScheduledBlockLocationServiceImpl.java:241-244 returns null when
	// scheduleTime is past the block's last stop; the arrivals bean then
	// leaves tripStatus fields at their defaults).
	InRange bool

	// Active trip = the latest block trip whose first stop has already passed.
	// Empty when currentTime is before any block trip starts.
	ActiveTripID                  string
	ActiveTripShape               []gtfs.ShapePoint
	ActiveTripCumulativeDistances []float64
	ActiveTripScheduledDistance   float64 // within-active-trip distance at currentTime
	ActiveTripTotalDistance       float64

	// ShiftTripIDs is the set of trip IDs in the shift the snapshot was
	// built for, after keepShiftContainingTrip filtering. Callers use it to
	// decide whether a live vehicle whose declared trip differs from the
	// queried one still counts as operating this arrival's block instance
	// — Java's BlockLocation flows down to every arrival in the same
	// BlockInstance, not just the vehicle's declared-trip row.
	ShiftTripIDs map[string]struct{}
}

type scheduledStopKey struct {
	TripID             string
	StopSequenceInTrip int
}

// blockTripData bundles everything we need from one block trip so we only hit
// the DB once per trip during snapshot construction.
type blockTripData struct {
	id           string
	stopTimes    []gtfsdb.StopTime
	shapePoints  []gtfs.ShapePoint
	cumDistances []float64
	totalDist    float64
	firstSeconds int64
	lastSeconds  int64
}

// snapshotCache memoizes computeScheduledBlockSnapshot results for the
// lifetime of a request. The plural arrivals handler processes many stop
// times whose trips often share a block; without this every row would pay
// for a fresh block snapshot (4 queries after emitBlockStops was batched)
// even though every trip in the same block produces the same snapshot.
//
// Keyed by (currentTime, serviceDate, tripID). currentTime and serviceDate
// are part of the key because the snapshot's interpolation depends on both;
// tripID is the resolvable identity of the shift. After a snapshot is built,
// every trip in the returned snapshot's Stops (block-mates that survived
// shift filtering) inherits the same cache entry so sibling lookups hit.
type snapshotCache struct {
	m map[snapshotCacheEntryKey]*scheduledBlockSnapshot
}

type snapshotCacheEntryKey struct {
	tripID          string
	currentTimeUnix int64
	serviceDateUnix int64
}

func newSnapshotCache() *snapshotCache {
	return &snapshotCache{m: make(map[snapshotCacheEntryKey]*scheduledBlockSnapshot)}
}

func makeSnapshotCacheKey(tripID string, currentTime, serviceDate time.Time) snapshotCacheEntryKey {
	return snapshotCacheEntryKey{
		tripID:          tripID,
		currentTimeUnix: currentTime.UnixNano(),
		serviceDateUnix: serviceDate.UnixNano(),
	}
}

type snapshotCacheKey struct{}

// WithSnapshotCache attaches cache to ctx so computeScheduledBlockSnapshot
// can memoize across BuildTripStatus calls without a signature change.
func WithSnapshotCache(ctx context.Context, cache *snapshotCache) context.Context {
	if cache == nil {
		return ctx
	}
	return context.WithValue(ctx, snapshotCacheKey{}, cache)
}

func snapshotCacheFrom(ctx context.Context) *snapshotCache {
	c, _ := ctx.Value(snapshotCacheKey{}).(*snapshotCache)
	return c
}

// computeScheduledBlockSnapshot builds a snapshot for the block that contains
// targetTripID. Trips with no block are treated as a one-trip block. Returns
// nil when no stop times can be loaded.
//
// Cost is 4 DB round-trips per uncached call: blockTripIDsForServiceDate (1),
// loadBlockTripData → GetStopTimesForTripIDs (1) + GetShapePointsByTripIDs (1),
// and emitBlockStops → GetStopsByIDs (1, batched across every block-trip).
// When ctx carries a snapshotCache (see WithSnapshotCache), block-mates of
// a previously-computed snapshot hit at zero DB cost; the plural arrivals
// handler installs one such cache per request.
func (api *RestAPI) computeScheduledBlockSnapshot(
	ctx context.Context,
	targetTripID string,
	currentTime time.Time,
	serviceDate time.Time,
) *scheduledBlockSnapshot {
	cache := snapshotCacheFrom(ctx)
	if hit, ok := lookupCachedSnapshot(cache, targetTripID, currentTime, serviceDate); ok {
		return hit
	}

	trips := api.loadShiftTrips(ctx, targetTripID, serviceDate)
	if len(trips) == 0 {
		return nil
	}

	stops, tripOffsets := api.emitBlockStops(ctx, trips)
	if len(stops) == 0 {
		return nil
	}

	snap := buildSnapshotFromStops(stops, trips, currentTime, serviceDate)
	assignActiveTrip(snap, trips, tripOffsets)
	populateSnapshotCache(cache, snap, trips, currentTime, serviceDate)
	return snap
}

// lookupCachedSnapshot returns (snap, true) when the request-scoped cache
// (see WithSnapshotCache) has a snapshot for this exact (tripID, currentTime,
// serviceDate) combo. Extracted from computeScheduledBlockSnapshot to keep
// that function's cognitive complexity under SonarCloud's threshold.
func lookupCachedSnapshot(
	cache *snapshotCache,
	targetTripID string,
	currentTime, serviceDate time.Time,
) (*scheduledBlockSnapshot, bool) {
	if cache == nil {
		return nil, false
	}
	snap, ok := cache.m[makeSnapshotCacheKey(targetTripID, currentTime, serviceDate)]
	return snap, ok
}

// loadShiftTrips resolves targetTripID's block, loads every trip's stop
// times / shape / bounds, sorts by first-stop time, and narrows to the
// shift (contiguous run of non-overlapping trips) that contains
// targetTripID. Returns nil when any stage produces no usable trips —
// mirrors Java's per-shift BlockConfigurationEntry split for feeds that
// reuse one block_id across every bus in a day.
func (api *RestAPI) loadShiftTrips(ctx context.Context, targetTripID string, serviceDate time.Time) []blockTripData {
	tripIDs := api.blockTripIDsForServiceDate(ctx, targetTripID, serviceDate)
	if len(tripIDs) == 0 {
		return nil
	}
	trips := api.loadBlockTripData(ctx, tripIDs)
	if len(trips) == 0 {
		return nil
	}
	slices.SortFunc(trips, func(a, b blockTripData) int {
		return cmp.Compare(a.firstSeconds, b.firstSeconds)
	})
	return keepShiftContainingTrip(trips, targetTripID)
}

// buildSnapshotFromStops assembles the block-level snapshot fields around a
// stops slice + trips list. keepShiftContainingTrip should have removed
// cross-shift overlaps that produce non-monotonic EffectiveStopSeconds, but
// a feed could still emit out-of-order stop times WITHIN a single trip.
// Never reorder — slice positions define BlockSequence — but switch
// NextStopIndex selection and interpolateBlockDistance to linear-scan
// fallbacks when monotonicity is broken so both agree on the same
// bracketing stops.
func buildSnapshotFromStops(
	stops []blockStopMetric,
	trips []blockTripData,
	currentTime, serviceDate time.Time,
) *scheduledBlockSnapshot {
	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	stopIndex := make(map[scheduledStopKey]int, len(stops))
	for i, s := range stops {
		stopIndex[scheduledStopKey{TripID: s.TripID, StopSequenceInTrip: s.StopSequenceInTrip}] = i
	}

	monotonic := stopsAreMonotonic(stops)
	shiftTripIDs := make(map[string]struct{}, len(trips))
	for _, t := range trips {
		shiftTripIDs[t.id] = struct{}{}
	}

	firstStopSec := stops[0].EffectiveStopSeconds
	lastStopSec := stops[len(stops)-1].EffectiveStopSeconds
	return &scheduledBlockSnapshot{
		Stops:              stops,
		StopIndex:          stopIndex,
		NextStopIndex:      findNextStopIndex(stops, currentSeconds, monotonic),
		DistanceAlongBlock: interpolateBlockDistance(stops, currentSeconds, monotonic),
		InRange:            currentSeconds >= firstStopSec && currentSeconds <= lastStopSec,
		ShiftTripIDs:       shiftTripIDs,
	}
}

// assignActiveTrip fills snap.ActiveTrip* fields by picking the latest trip
// whose block-start offset is ≤ snap.DistanceAlongBlock. Mirrors Java's
// getScheduledBlockLocationBetweenStopTimes:337-352 — once the interpolated
// block-position crosses a later trip's start offset, that trip becomes
// active. Time-based selection drifts from Java at trip transitions when
// the vehicle is running late or early (the deviation shift moves
// DistanceAlongBlock across the boundary before clock time crosses the next
// trip's firstSeconds).
func assignActiveTrip(snap *scheduledBlockSnapshot, trips []blockTripData, tripOffsets []float64) {
	activeIdx := -1
	for i, offset := range tripOffsets {
		if snap.DistanceAlongBlock >= offset {
			activeIdx = i
		}
	}
	if activeIdx < 0 {
		return
	}
	active := trips[activeIdx]
	snap.ActiveTripID = active.id
	snap.ActiveTripShape = active.shapePoints
	snap.ActiveTripCumulativeDistances = active.cumDistances
	snap.ActiveTripTotalDistance = active.totalDist
	snap.ActiveTripScheduledDistance = math.Max(0, snap.DistanceAlongBlock-tripOffsets[activeIdx])
}

// populateSnapshotCache indexes snap under every trip in the shift so later
// BuildTripStatus calls for block-mates within the same request skip the
// whole compute path. No-op when the request didn't install a cache.
func populateSnapshotCache(
	cache *snapshotCache,
	snap *scheduledBlockSnapshot,
	trips []blockTripData,
	currentTime, serviceDate time.Time,
) {
	if cache == nil {
		return
	}
	for _, t := range trips {
		cache.m[makeSnapshotCacheKey(t.id, currentTime, serviceDate)] = snap
	}
}

// emitBlockStops walks the block trips in order, projecting each trip's
// stops onto its shape and emitting one blockStopMetric per stop with
// cumulative DistanceAlongBlock and BlockSequence. Returns the assembled
// slice and the per-trip block-start offsets so callers can pick the
// active trip by comparing DistanceAlongBlock to those offsets — Java's
// ScheduledBlockLocationServiceImpl.getScheduledBlockLocationBetweenStop
// Times:337-352 picks by distance-along-block, not clock time.
//
// Stop coords for every block-trip are batched into a single GetStopsByIDs
// call. Block routes almost always revisit shared terminal / transfer stops,
// so deduplicating across trips before the DB round-trip both saves N-1
// queries per snapshot and shrinks the working set.
func (api *RestAPI) emitBlockStops(ctx context.Context, trips []blockTripData) ([]blockStopMetric, []float64) {
	stops := make([]blockStopMetric, 0, len(trips)*40)
	tripOffsets := make([]float64, len(trips))

	// One DB call for the union of every block-trip's stops.
	unionStopTimes := make([]gtfsdb.StopTime, 0, len(trips)*40)
	for _, t := range trips {
		unionStopTimes = append(unionStopTimes, t.stopTimes...)
	}
	// Block metrics degrade to zero distances rather than failing the
	// snapshot; the helper logs the cause.
	stopByID, _ := api.fetchStopCoordsForStopTimes(ctx, unionStopTimes)

	var cumulativeBlockDist float64
	var blockSeq int
	for i, t := range trips {
		tripOffsets[i] = cumulativeBlockDist
		var tripStopDistances []float64
		var tripLength float64
		if len(t.shapePoints) >= 2 {
			tripStopDistances = projectStopsInSequence(t.stopTimes, stopByID, t.shapePoints, t.cumDistances)
			tripLength = t.totalDist
		} else {
			// Shapeless trip (missing shape_id, unresolvable shape, or shape
			// has <2 points). Java's StopTimeEntriesFactory.ensureStopTimes
			// HaveShapeDistanceTraveledSet:266-280 falls back to cumulative
			// haversine between consecutive stops; we do the same so the
			// trip contributes a real length to the block cursor and every
			// stop gets a distinct DistanceAlongBlock. Without this the
			// trip collapses to zero and every later trip in the block is
			// short by this trip's length.
			tripStopDistances, tripLength = haversineStopDistances(t.stopTimes, stopByID)
		}
		for k, st := range t.stopTimes {
			stops = append(stops, blockStopMetric{
				TripID:               t.id,
				StopID:               st.StopID,
				StopSequenceInTrip:   int(st.StopSequence),
				BlockSequence:        blockSeq,
				EffectiveStopSeconds: utils.EffectiveStopTimeSeconds(st.ArrivalTime, st.DepartureTime),
				DistanceAlongBlock:   cumulativeBlockDist + tripStopDistances[k],
				DistanceAlongTrip:    tripStopDistances[k],
			})
			blockSeq++
		}
		cumulativeBlockDist += tripLength
	}
	return stops, tripOffsets
}

// metricsForStop is the Java applyBlockLocationToBean formula:
//
//	distanceFromStop  = targetStopDistanceAlongBlock − snapshotDistanceAlongBlock
//	numberOfStopsAway = targetStopBlockSequence − nextStopBlockSequence
//
// Returns ok=false (callers leave both at zero) when:
//   - target stop isn't on the block, or
//   - NextStopIndex<0 (currentTime is past the block's last stop — Java's
//     getScheduledBlockLocationFromScheduledTime returns null, which
//     short-circuits applyBlockLocationToBean; without this guard our
//     snapshot clamps to the last stop's distance, producing "bus is 7 km
//     past your stop" for trips that ended hours ago), or
//   - !InRange (currentTime is before or after the block's schedule
//     window). BuildTripStatus deliberately leaves the tripStatus distance
//     fields at their defaults when InRange is false; callers of this
//     method must apply the same gate or the arrival's distanceFromStop /
//     numberOfStopsAway will report a clamped-to-block-start position for
//     a bus that hasn't left the depot yet — e.g. "14 stops away" on an
//     08:00 request for a trip departing 23:00 the same service day.
func (s *scheduledBlockSnapshot) metricsForStop(
	tripID string,
	stopSequenceInTrip int,
) (distanceFromStop float64, numberOfStopsAway int, ok bool) {
	idx, found := s.StopIndex[scheduledStopKey{TripID: tripID, StopSequenceInTrip: stopSequenceInTrip}]
	if !found {
		return 0, 0, false
	}
	if s.NextStopIndex < 0 {
		return 0, 0, false
	}
	if !s.InRange {
		return 0, 0, false
	}
	target := s.Stops[idx]
	distanceFromStop = target.DistanceAlongBlock - s.DistanceAlongBlock
	numberOfStopsAway = target.BlockSequence - s.Stops[s.NextStopIndex].BlockSequence
	return distanceFromStop, numberOfStopsAway, true
}

// applyScheduledTripPositionToStatus is the fallback for the rare case where
// the block snapshot has no active trip (currentTime falls before every block
// trip starts). It interpolates within the target trip only, so position and
// scheduledDistanceAlongTrip get reasonable zero-clamped values instead of the
// (0, 0) lat/lon default.
func (api *RestAPI) applyScheduledTripPositionToStatus(
	ctx context.Context,
	status *models.TripStatus,
	stopTimes []gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
	currentTime time.Time,
	serviceDate time.Time,
) {
	if len(stopTimes) == 0 || len(shapePoints) < 2 || len(cumulativeDistances) != len(shapePoints) {
		return
	}
	// Block metrics degrade to zero distances rather than failing the
	// snapshot; the helper logs the cause.
	stopByID, _ := api.fetchStopCoordsForStopTimes(ctx, stopTimes)
	pos, orient, dist := scheduledTripPosition(
		stopTimes, stopByID, shapePoints, cumulativeDistances, currentTime, serviceDate)
	status.ScheduledDistanceAlongTrip = dist

	if pos != nil {
		status.Position = *pos
		if orient >= 0 {
			status.Orientation = orient
		}
	}
}

// scheduledTripPosition projects a trip's schedule onto its shape at
// currentTime: where along the shape the trip should be, its orientation
// there, and the distance along the trip that position represents.
// cumulativeDistances is precalculated by the caller since some callers need
// it for more than this one projection.
func scheduledTripPosition(
	stopTimes []gtfsdb.StopTime,
	stopByID map[string]gtfsdb.Stop,
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
	currentTime time.Time,
	serviceDate time.Time,
) (position *models.Location, orientation float64, distanceAlongTrip float64) {
	stopDistances := projectStopsInSequence(stopTimes, stopByID, shapePoints, cumulativeDistances)

	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)
	distanceAlongTrip = interpolateDistanceAtScheduledTime(currentSeconds, stopTimes, stopDistances)

	position, orientation = positionAndOrientationAtDistance(shapePoints, cumulativeDistances, distanceAlongTrip)
	return position, orientation, distanceAlongTrip
}

// keepShiftContainingTrip splits the time-sorted block trips at temporal
// overlaps (where a later trip starts before the previous one ends — impossible
// for a single physical bus) and returns only the contiguous "shift" that
// contains targetTripID. Returns nil if the target isn't in the slice.
// Mirrors what Java's bundle build does for BlockConfigurationEntry boundaries.
func keepShiftContainingTrip(trips []blockTripData, targetTripID string) []blockTripData {
	if len(trips) == 0 {
		return nil
	}
	start := 0
	end := len(trips)
	targetIdx := -1
	for i, t := range trips {
		if t.id == targetTripID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return nil
	}
	// Walk back from the target while consecutive trips don't overlap.
	for i := targetIdx; i > 0; i-- {
		if trips[i].firstSeconds < trips[i-1].lastSeconds {
			start = i
			break
		}
	}
	// Walk forward from the target while consecutive trips don't overlap.
	for i := targetIdx; i < len(trips)-1; i++ {
		if trips[i+1].firstSeconds < trips[i].lastSeconds {
			end = i + 1
			break
		}
	}
	return trips[start:end]
}

// blockTripIDsForServiceDate returns the IDs of trips that share targetTripID's
// block and are active on serviceDate. Falls back to [targetTripID] when the
// trip has no block or block lookup fails.
func (api *RestAPI) blockTripIDsForServiceDate(
	ctx context.Context,
	targetTripID string,
	serviceDate time.Time,
) []string {
	// Distinguish "this trip legitimately has no block" (sql.ErrNoRows + invalid
	// nullable) from "DB blip" so that infrastructure problems don't silently
	// degrade the snapshot to single-trip mode. The single-trip fallback IS
	// the right behaviour for the not-found cases — it just shouldn't be
	// reached on real DB errors without a warning.
	fallback := []string{targetTripID}
	q := api.GtfsManager.GtfsDB.Queries

	blockID, err := q.GetBlockIDByTripID(ctx, targetTripID)
	if err != nil {
		warnIfRealDBError(err, "blockTripIDsForServiceDate: GetBlockIDByTripID failed, degrading to single-trip mode",
			slog.String("trip_id", targetTripID))
		return fallback
	}
	blockIDStr := nulls.StringOrEmpty(blockID)
	if blockIDStr == "" {
		return fallback
	}
	blockTrips, err := q.GetTripsByBlockID(ctx, blockID)
	if err != nil {
		warnIfRealDBError(err, "blockTripIDsForServiceDate: GetTripsByBlockID failed, degrading to single-trip mode",
			slog.String("trip_id", targetTripID), slog.String("block_id", blockIDStr))
		return fallback
	}
	if len(blockTrips) == 0 {
		return fallback
	}
	activeServiceIDs, err := q.GetActiveServiceIDsForDate(ctx, serviceDate.Format("20060102"))
	if err != nil {
		warnIfRealDBError(err, "blockTripIDsForServiceDate: GetActiveServiceIDsForDate failed, degrading to single-trip mode",
			slog.String("trip_id", targetTripID), slog.String("date", serviceDate.Format("20060102")))
		return fallback
	}
	activeSet := make(map[string]struct{}, len(activeServiceIDs))
	for _, id := range activeServiceIDs {
		activeSet[id] = struct{}{}
	}
	ids := make([]string, 0, len(blockTrips))
	for _, bt := range blockTrips {
		if _, ok := activeSet[bt.ServiceID]; ok {
			ids = append(ids, bt.ID)
		}
	}
	if len(ids) == 0 {
		return fallback
	}
	return ids
}

// loadBlockTripData fetches stop_times + shape for every tripID in one pair
// of batched queries (GetStopTimesForTripIDs + GetShapePointsByTripIDs)
// instead of the per-trip pair. For a block with N trips this is 2 DB
// round-trips instead of 2N — a real handle on the plural arrivals
// handler's amplification.
//
// Trips with no stop_times are skipped. Shape errors / missing shapes
// leave shapePoints empty; emitBlockStops falls back to stop-only
// haversine (haversineStopDistances) so shapeless trips still contribute
// a real length to the block cursor. Trip is still appended so
// block_sequence stays consistent.
func (api *RestAPI) loadBlockTripData(ctx context.Context, tripIDs []string) []blockTripData {
	if len(tripIDs) == 0 {
		return nil
	}
	q := api.GtfsManager.GtfsDB.Queries

	stopTimeRows, err := q.GetStopTimesForTripIDs(ctx, tripIDs)
	if err != nil {
		return nil
	}
	stopTimesByTrip := make(map[string][]gtfsdb.StopTime, len(tripIDs))
	for _, st := range stopTimeRows {
		stopTimesByTrip[st.TripID] = append(stopTimesByTrip[st.TripID], st)
	}

	// Shape errors are non-fatal — emitBlockStops falls back to haversine.
	// A total failure of the batch query means we degrade every trip to
	// the fallback, still correct just less precise. Log real DB errors
	// so an operator sees the degradation instead of silently losing
	// shape-based precision across every block trip.
	shapeRows, err := q.GetShapePointsByTripIDs(ctx, tripIDs)
	warnIfRealDBError(err, "loadBlockTripData: GetShapePointsByTripIDs failed, degrading every trip to haversine fallback",
		slog.Int("trip_count", len(tripIDs)))
	shapePointsByTrip := make(map[string][]gtfs.ShapePoint, len(tripIDs))
	for _, sr := range shapeRows {
		shapePointsByTrip[sr.TripID] = append(shapePointsByTrip[sr.TripID],
			gtfs.ShapePoint{Latitude: sr.Lat, Longitude: sr.Lon})
	}

	out := make([]blockTripData, 0, len(tripIDs))
	for _, id := range tripIDs {
		stopTimes := stopTimesByTrip[id]
		if len(stopTimes) == 0 {
			continue
		}
		shapePoints := shapePointsByTrip[id]
		var cumDistances []float64
		var totalDist float64
		if len(shapePoints) >= 2 {
			cumDistances = preCalculateCumulativeDistances(shapePoints)
			totalDist = cumDistances[len(cumDistances)-1]
		}
		out = append(out, blockTripData{
			id:           id,
			stopTimes:    stopTimes,
			shapePoints:  shapePoints,
			cumDistances: cumDistances,
			totalDist:    totalDist,
			firstSeconds: utils.EffectiveStopTimeSeconds(stopTimes[0].ArrivalTime, stopTimes[0].DepartureTime),
			lastSeconds: utils.EffectiveStopTimeSeconds(
				stopTimes[len(stopTimes)-1].ArrivalTime,
				stopTimes[len(stopTimes)-1].DepartureTime,
			),
		})
	}
	return out
}

// fetchStopCoordsForStopTimes fetches the unique stops in stopTimes, batching
// the lookup so an unbounded caller — the scheduled-position candidate set
// among them — cannot overflow the bind variable limit. Returns the error on
// failure; a request-path caller should propagate it rather than degrade
// silently. Block-snapshot callers that intentionally tolerate a degraded
// result discard the error explicitly at the call site.
func (api *RestAPI) fetchStopCoordsForStopTimes(
	ctx context.Context,
	stopTimes []gtfsdb.StopTime,
) (map[string]gtfsdb.Stop, error) {
	seen := make(map[string]struct{}, len(stopTimes))
	ids := make([]string, 0, len(stopTimes))
	for _, st := range stopTimes {
		if _, ok := seen[st.StopID]; ok {
			continue
		}
		seen[st.StopID] = struct{}{}
		ids = append(ids, st.StopID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	stops, err := queryInBatches(ctx, ids, api.GtfsManager.GtfsDB.Queries.GetStopsByIDs)
	if err != nil {
		slog.Warn("fetchStopCoordsForStopTimes: GetStopsByIDs failed",
			slog.Int("stop_count", len(ids)), slog.String("error", err.Error()))
		return nil, err
	}
	byID := make(map[string]gtfsdb.Stop, len(stops))
	for _, s := range stops {
		byID[s.ID] = s
	}
	return byID, nil
}

// projectStopsInSequence returns each stop's distance-along-trip in metres,
// projecting the stop's lat/lon onto the shape polyline. The publisher's
// stop_times.shape_dist_traveled is deliberately ignored — Java's
// StopTimeEntriesFactory.ensureStopTimesHaveShapeDistanceTraveledSet does
// the same, overwriting the publisher value at load time with the projected
// value from DistanceAlongShapeLibrary. Publisher-supplied values are often
// wrong (bad units, corrupt data on some feeds), so both codebases treat
// projection as authoritative.
//
// Our projection uses a monotonic cursor so loop routes (same lat/lon at
// multiple shape segments) get distinct distances per occurrence; a naive
// global-minimum search picks the same segment for both occurrences,
// producing the catastrophic distanceFromStop outliers we saw on the Q
// route. Java's DistanceAlongShapeLibrary uses a more sophisticated
// UTM-based multi-assignment algorithm — matching Java's exact numbers
// would require porting that library; our simpler projection produces
// values within ~90-250m of Java's, which is acceptable drift for our use.
func projectStopsInSequence(
	stopTimes []gtfsdb.StopTime,
	stopByID map[string]gtfsdb.Stop,
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
) []float64 {
	distances := make([]float64, len(stopTimes))
	if len(shapePoints) < 2 || len(cumulativeDistances) != len(shapePoints) {
		return distances
	}

	lastMatchedIndex := 0
	for i, st := range stopTimes {
		stop, ok := stopByID[st.StopID]
		if !ok {
			distances[i] = 0
			continue
		}
		distances[i], lastMatchedIndex = projectStopGeometric(stop, shapePoints, cumulativeDistances, lastMatchedIndex)
	}
	return distances
}

// haversineStopDistances is the fallback used when a trip has no shape
// polyline (missing shape_id, GetShapePointsByTripID error, or fewer than
// two shape points). It accumulates the great-circle distance between
// consecutive stops using their static lat/lon and returns per-stop
// cumulative distances plus the trip's total length.
//
// Mirrors Java's StopTimeEntriesFactory.ensureStopTimesHaveShapeDistance
// TraveledSet:266-280, which does the same "make do without" cumulative
// haversine when shape data is unavailable. Without this fallback,
// shapeless trips collapse to a per-stop distance of zero and their
// entire length disappears from the block cursor — every later trip in
// the block is then short by the missing trip's length.
//
// Returns a zero-filled slice and length 0 only when a stop's static
// coordinates cannot be resolved (unknown stop_id in stopByID); at that
// point we can't do any better than the pre-fix behaviour.
func haversineStopDistances(stopTimes []gtfsdb.StopTime, stopByID map[string]gtfsdb.Stop) ([]float64, float64) {
	distances := make([]float64, len(stopTimes))
	if len(stopTimes) == 0 {
		return distances, 0
	}
	var cumulative float64
	prev, prevOK := stopByID[stopTimes[0].StopID]
	distances[0] = 0
	for i := 1; i < len(stopTimes); i++ {
		curr, currOK := stopByID[stopTimes[i].StopID]
		if prevOK && currOK {
			cumulative += utils.Distance(prev.Lat, prev.Lon, curr.Lat, curr.Lon)
		}
		distances[i] = cumulative
		prev, prevOK = curr, currOK
	}
	return distances, cumulative
}

// projectStopGeometric projects a stop's lat/lon onto the shape, scanning
// forward from `cursor` to preserve monotonicity on loop routes. Returns
// the projected distance and the updated cursor.
func projectStopGeometric(stop gtfsdb.Stop, shapePoints []gtfs.ShapePoint, cumulativeDistances []float64, cursor int) (float64, int) {
	const earlyExitThresholdMeters = 100.0
	const goodMatchThreshold = 500.0

	if cursor >= len(shapePoints)-1 {
		cursor = len(shapePoints) - 2
	}
	minDistance := math.Inf(1)
	closestSegmentIndex := cursor
	var projectionRatio float64
	for j := cursor; j < len(shapePoints)-1; j++ {
		d, ratio := distanceToLineSegment(
			stop.Lat, stop.Lon,
			shapePoints[j].Latitude, shapePoints[j].Longitude,
			shapePoints[j+1].Latitude, shapePoints[j+1].Longitude,
		)
		if d < minDistance {
			minDistance = d
			closestSegmentIndex = j
			projectionRatio = ratio
			cursor = j
		} else if minDistance < goodMatchThreshold && d > minDistance+earlyExitThresholdMeters {
			break
		}
	}
	// Loop-route correctness: when the best match lands at the END of a
	// segment (ratio ≈ 1.0), advance the cursor past that segment so the
	// next stop doesn't snap back to it. Without this, a stop whose coords
	// repeat earlier on the shape (figure-eight, lasso, Q-route loop) finds
	// the same zero-distance match at the original segment and gets
	// distance 0 instead of progressing along the loop.
	if projectionRatio > 0.95 && cursor < len(shapePoints)-2 {
		cursor++
	}
	var segmentLength float64
	if closestSegmentIndex < len(shapePoints)-1 {
		segmentLength = utils.Distance(
			shapePoints[closestSegmentIndex].Latitude, shapePoints[closestSegmentIndex].Longitude,
			shapePoints[closestSegmentIndex+1].Latitude, shapePoints[closestSegmentIndex+1].Longitude,
		)
	}
	return interpolateDistance(cumulativeDistances, segmentLength, closestSegmentIndex, projectionRatio), cursor
}

// stopsAreMonotonic reports whether every stop's EffectiveStopSeconds is
// non-decreasing along the slice. Cross-shift overlaps or malformed stop-time
// pairs can break the invariant, in which case binary search yields wrong
// bracketing pairs; callers must fall back to a linear scan.
func stopsAreMonotonic(stops []blockStopMetric) bool {
	for i := 1; i < len(stops); i++ {
		if stops[i].EffectiveStopSeconds < stops[i-1].EffectiveStopSeconds {
			return false
		}
	}
	return true
}

// findNextStopIndex returns the index of the first stop whose EffectiveStop
// Seconds is >= currentSeconds, or -1 when currentSeconds is past every stop.
// Uses binary search when stops are monotonic; falls back to a linear scan
// otherwise so the answer stays correct even on malformed inputs.
func findNextStopIndex(stops []blockStopMetric, currentSeconds int64, monotonic bool) int {
	if len(stops) == 0 {
		return -1
	}
	if monotonic {
		idx, _ := slices.BinarySearchFunc(stops, currentSeconds, func(s blockStopMetric, target int64) int {
			return cmp.Compare(s.EffectiveStopSeconds, target)
		})
		if idx < len(stops) {
			return idx
		}
		return -1
	}
	for i, s := range stops {
		if s.EffectiveStopSeconds >= currentSeconds {
			return i
		}
	}
	return -1
}

// interpolateBlockDistance linearly interpolates the block's distance-along-block
// at currentSeconds between the two surrounding stops. Clamped to the first /
// last stop when currentSeconds is outside the block's scheduled span.
//
// When monotonic is true, stops are sorted by EffectiveStopSeconds and we
// binary-search the bracketing pair in O(log N). Otherwise we linear-scan so
// the bracketing pair is still correct on out-of-order stop times.
func interpolateBlockDistance(stops []blockStopMetric, currentSeconds int64, monotonic bool) float64 {
	if len(stops) == 0 {
		return 0
	}
	if currentSeconds <= stops[0].EffectiveStopSeconds {
		return stops[0].DistanceAlongBlock
	}
	last := stops[len(stops)-1]
	if currentSeconds >= last.EffectiveStopSeconds {
		return last.DistanceAlongBlock
	}
	var from, to blockStopMetric
	if monotonic {
		// Find the first stop whose time is > currentSeconds; `to` is that
		// stop, `from` is the one before it. Guaranteed to be in
		// [1, len(stops)-1] because we already clamped both endpoints above.
		idx, _ := slices.BinarySearchFunc(stops, currentSeconds, func(s blockStopMetric, target int64) int {
			return cmp.Compare(s.EffectiveStopSeconds, target)
		})
		if idx == 0 {
			idx = 1
		}
		from = stops[idx-1]
		to = stops[idx]
	} else {
		// Linear-scan fallback: pick the last stop whose time is <=
		// currentSeconds as `from`, and the next stop as `to`.
		idx := 1
		for i := 1; i < len(stops); i++ {
			if stops[i].EffectiveStopSeconds > currentSeconds {
				idx = i
				break
			}
			idx = i
		}
		from = stops[idx-1]
		to = stops[idx]
	}
	span := to.EffectiveStopSeconds - from.EffectiveStopSeconds
	if span == 0 {
		return from.DistanceAlongBlock
	}
	ratio := float64(currentSeconds-from.EffectiveStopSeconds) / float64(span)
	return from.DistanceAlongBlock + ratio*(to.DistanceAlongBlock-from.DistanceAlongBlock)
}

// positionAndOrientationAtDistance projects a distance-along-shape back to a
// lat/lon and infers the shape segment's heading at that point. Returns
// (nil, -1) when the shape is unusable.
func positionAndOrientationAtDistance(
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
	distance float64,
) (*models.Location, float64) {
	if len(shapePoints) < 2 || len(cumulativeDistances) != len(shapePoints) {
		return nil, -1
	}
	if distance <= 0 {
		return &models.Location{Lat: shapePoints[0].Latitude, Lon: shapePoints[0].Longitude},
			segmentOrientation(shapePoints[0], shapePoints[1])
	}
	last := cumulativeDistances[len(cumulativeDistances)-1]
	if distance >= last {
		end := shapePoints[len(shapePoints)-1]
		prev := shapePoints[len(shapePoints)-2]
		return &models.Location{Lat: end.Latitude, Lon: end.Longitude}, segmentOrientation(prev, end)
	}
	for i := 0; i < len(cumulativeDistances)-1; i++ {
		segStart, segEnd := cumulativeDistances[i], cumulativeDistances[i+1]
		if distance >= segStart && distance <= segEnd {
			span := segEnd - segStart
			if span == 0 {
				return &models.Location{Lat: shapePoints[i].Latitude, Lon: shapePoints[i].Longitude},
					segmentOrientation(shapePoints[i], shapePoints[i+1])
			}
			ratio := (distance - segStart) / span
			from, to := shapePoints[i], shapePoints[i+1]
			return &models.Location{
					Lat: from.Latitude + ratio*(to.Latitude-from.Latitude),
					Lon: from.Longitude + ratio*(to.Longitude-from.Longitude),
				},
				segmentOrientation(from, to)
		}
	}
	return nil, -1
}
