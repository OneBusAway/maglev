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

	// Active trip = the latest block trip whose first stop has already passed.
	// Empty when currentTime is before any block trip starts.
	ActiveTripID                  string
	ActiveTripShape               []gtfs.ShapePoint
	ActiveTripCumulativeDistances []float64
	ActiveTripScheduledDistance   float64 // within-active-trip distance at currentTime
	ActiveTripTotalDistance       float64
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

// computeScheduledBlockSnapshot builds a snapshot for the block that contains
// targetTripID. Trips with no block are treated as a one-trip block. Returns
// nil when no stop times can be loaded.
//
// TODO(perf): each call issues ~(3 + 3N) DB queries where N = block size, and
// the plural arrivals handler calls this twice per arrival row.
func (api *RestAPI) computeScheduledBlockSnapshot(
	ctx context.Context,
	targetTripID string,
	currentTime time.Time,
	serviceDate time.Time,
) *scheduledBlockSnapshot {
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
	// Some feeds reuse one block_id across every bus in a day. Java's
	// bundle build splits these into per-shift BlockConfigurationEntries;
	// we replicate at query time.
	trips = keepShiftContainingTrip(trips, targetTripID)
	if len(trips) == 0 {
		return nil
	}

	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	// Pick the active trip: the latest one whose first stop has already passed.
	// Java's BlockLocation keeps the prior trip as "active" during the gap
	// between two consecutive trips, hence the loose `>= firstSeconds` test.
	activeIdx := -1
	for i, t := range trips {
		if currentSeconds >= t.firstSeconds {
			activeIdx = i
		}
	}

	stops, activeTripOffset := api.emitBlockStops(ctx, trips, activeIdx)
	if len(stops) == 0 {
		return nil
	}

	stopIndex := make(map[scheduledStopKey]int, len(stops))
	for i, s := range stops {
		stopIndex[scheduledStopKey{TripID: s.TripID, StopSequenceInTrip: s.StopSequenceInTrip}] = i
	}

	// TODO(perf): linear scan; stops are sorted by EffectiveStopSeconds so
	// this can be slices.BinarySearchFunc — O(log N) instead of O(N).
	nextStopIdx := -1
	for i, s := range stops {
		if s.EffectiveStopSeconds >= currentSeconds {
			nextStopIdx = i
			break
		}
	}

	snap := &scheduledBlockSnapshot{
		Stops:              stops,
		StopIndex:          stopIndex,
		NextStopIndex:      nextStopIdx,
		DistanceAlongBlock: interpolateBlockDistance(stops, currentSeconds),
	}
	if activeIdx >= 0 {
		active := trips[activeIdx]
		snap.ActiveTripID = active.id
		snap.ActiveTripShape = active.shapePoints
		snap.ActiveTripCumulativeDistances = active.cumDistances
		snap.ActiveTripTotalDistance = active.totalDist
		snap.ActiveTripScheduledDistance = math.Max(0, snap.DistanceAlongBlock-activeTripOffset)
	}
	return snap
}

// emitBlockStops walks the block trips in order, projecting each trip's
// stops onto its shape and emitting one blockStopMetric per stop with
// cumulative DistanceAlongBlock and BlockSequence. Returns the assembled
// slice and the activeTripOffset (block-distance to the start of the
// active trip; 0 when activeIdx < 0).
func (api *RestAPI) emitBlockStops(ctx context.Context, trips []blockTripData, activeIdx int) ([]blockStopMetric, float64) {
	stops := make([]blockStopMetric, 0, len(trips)*40)
	var cumulativeBlockDist float64
	var blockSeq int
	var activeTripOffset float64
	for i, t := range trips {
		if i == activeIdx {
			activeTripOffset = cumulativeBlockDist
		}
		// TODO(perf): hoist out of the loop; fetch the union of stop IDs once.
		stopByID := api.fetchStopCoordsForStopTimes(ctx, t.stopTimes)
		tripStopDistances := projectStopsInSequence(t.stopTimes, stopByID, t.shapePoints, t.cumDistances)
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
		cumulativeBlockDist += t.totalDist
	}
	return stops, activeTripOffset
}

// metricsForStop is the Java applyBlockLocationToBean formula:
//
//	distanceFromStop  = targetStopDistanceAlongBlock − snapshotDistanceAlongBlock
//	numberOfStopsAway = targetStopBlockSequence − nextStopBlockSequence
//
// Returns ok=false (callers leave both at zero) when target stop isn't on the
// block, or when NextStopIndex<0 — Java's
// getScheduledBlockLocationFromScheduledTime returns null past the block's
// last stop, which short-circuits applyBlockLocationToBean. Without this
// guard our snapshot clamps to the last stop's distance, producing
// "bus is 7 km past your stop" for trips that ended hours ago.
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
	stopByID := api.fetchStopCoordsForStopTimes(ctx, stopTimes)
	stopDistances := projectStopsInSequence(stopTimes, stopByID, shapePoints, cumulativeDistances)

	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)
	scheduledDist := interpolateDistanceAtScheduledTime(currentSeconds, stopTimes, stopDistances)
	status.ScheduledDistanceAlongTrip = scheduledDist

	if pos, orient := positionAndOrientationAtDistance(shapePoints, cumulativeDistances, scheduledDist); pos != nil {
		status.Position = *pos
		if orient >= 0 {
			status.Orientation = orient
		}
	}
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

// loadBlockTripData fetches stop_times + shape for each tripID and bundles them.
// Skips trips with no stop times or unusable shape data.
//
// TODO(perf): 2N round-trips; batch via GetStopTimesForTrips / GetShapePointsForTrips.
func (api *RestAPI) loadBlockTripData(ctx context.Context, tripIDs []string) []blockTripData {
	out := make([]blockTripData, 0, len(tripIDs))
	for _, id := range tripIDs {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, id)
		if err != nil || len(stopTimes) == 0 {
			continue
		}
		// TODO(correctness): shape errors leave totalDist=0 — trip is still
		// appended so block_sequence stays consistent, but its DistanceAlongTrip
		// values are zero. Stop-only Haversine fallback would fix this.
		shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, id)
		shapePoints := shapeRowsToPoints(shapeRows)
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

// fetchStopCoordsForStopTimes batches GetStopsByIDs for the unique stop IDs in
// stopTimes. Returns nil on error; callers fall back to zero distance.
func (api *RestAPI) fetchStopCoordsForStopTimes(
	ctx context.Context,
	stopTimes []gtfsdb.StopTime,
) map[string]gtfsdb.Stop {
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
		return nil
	}
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, ids)
	if err != nil {
		// Returning nil here causes projectStopsInSequence to write 0 for
		// every geometric-projection stop, silently corrupting downstream
		// DistanceAlongTrip. Surface the error in logs so operators see
		// infrastructure issues; the snapshot still proceeds in degraded mode.
		slog.Warn("fetchStopCoordsForStopTimes: GetStopsByIDs failed, projection distances will be zero",
			slog.Int("stop_count", len(ids)), slog.String("error", err.Error()))
		return nil
	}
	byID := make(map[string]gtfsdb.Stop, len(stops))
	for _, s := range stops {
		byID[s.ID] = s
	}
	return byID
}

// projectStopsInSequence returns each stop's distance-along-trip in metres,
// projecting monotonically through the shape so loop routes (where the same
// lat/lon appears at multiple shape segments) get distinct distances per
// occurrence. Mirrors Java's DistanceAlongShapeLibrary.computeBestAssignment;
// a naive global-minimum search picks the same segment for both occurrences,
// producing the catastrophic distanceFromStop outliers we saw on the Q route.
//
// Prefers GTFS shape_dist_traveled (scaled to metres) when present, falls
// back to forward-walking geometric projection.
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
	shapeDistScale := computeShapeDistScale(stopTimes, cumulativeDistances)

	lastMatchedIndex := 0
	for i, st := range stopTimes {
		// Prefer GTFS shape_dist_traveled when available; it's authoritative
		// for loops and uniquely identifies the right occurrence.
		if st.ShapeDistTraveled.Valid {
			distances[i] = st.ShapeDistTraveled.Float64 * shapeDistScale
			lastMatchedIndex = advanceCursorThroughCumDist(lastMatchedIndex, cumulativeDistances, distances[i])
			continue
		}
		stop, ok := stopByID[st.StopID]
		if !ok {
			distances[i] = 0
			continue
		}
		distances[i], lastMatchedIndex = projectStopGeometric(stop, shapePoints, cumulativeDistances, lastMatchedIndex)
	}
	return distances
}

// computeShapeDistScale derives a per-trip scale factor so publisher units
// (km/miles/...) come out as metres. Identical to calculateBatchStopDistances.
func computeShapeDistScale(stopTimes []gtfsdb.StopTime, cumulativeDistances []float64) float64 {
	totalShapeDist := cumulativeDistances[len(cumulativeDistances)-1]
	var maxStopDist float64
	for _, st := range stopTimes {
		if st.ShapeDistTraveled.Valid && st.ShapeDistTraveled.Float64 > maxStopDist {
			maxStopDist = st.ShapeDistTraveled.Float64
		}
	}
	if maxStopDist > 0 && totalShapeDist > 0 {
		return totalShapeDist / maxStopDist
	}
	return 1.0
}

// advanceCursorThroughCumDist moves the monotonic cursor forward through the
// shape to the segment that contains `distance`. Used after the
// shape_dist_traveled branch so subsequent geometric stops don't regress.
func advanceCursorThroughCumDist(cursor int, cumulativeDistances []float64, distance float64) int {
	for j := cursor; j < len(cumulativeDistances)-1; j++ {
		if cumulativeDistances[j+1] >= distance {
			return j
		}
	}
	return cursor
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

// interpolateBlockDistance linearly interpolates the block's distance-along-block
// at currentSeconds between the two surrounding stops. Clamped to the first /
// last stop when currentSeconds is outside the block's scheduled span.
//
// TODO(perf): linear scan over `stops`; binary-search the bracketing pair.
func interpolateBlockDistance(stops []blockStopMetric, currentSeconds int64) float64 {
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
	for i := 0; i < len(stops)-1; i++ {
		from, to := stops[i], stops[i+1]
		if currentSeconds >= from.EffectiveStopSeconds && currentSeconds <= to.EffectiveStopSeconds {
			span := to.EffectiveStopSeconds - from.EffectiveStopSeconds
			if span == 0 {
				return from.DistanceAlongBlock
			}
			ratio := float64(currentSeconds-from.EffectiveStopSeconds) / float64(span)
			return from.DistanceAlongBlock + ratio*(to.DistanceAlongBlock-from.DistanceAlongBlock)
		}
	}
	return last.DistanceAlongBlock
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
