package restapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

// Pure-function tests — no DB or test API needed.
func TestInterpolateBlockDistance_EmptyStops(t *testing.T) {
	assert.Equal(t, 0.0, interpolateBlockDistance(nil, 1000))
}

func TestInterpolateBlockDistance_BeforeFirstStop(t *testing.T) {
	stops := []blockStopMetric{
		{EffectiveStopSeconds: 100, DistanceAlongBlock: 0},
		{EffectiveStopSeconds: 200, DistanceAlongBlock: 500},
	}
	// currentSeconds = 50 < first.EffectiveStopSeconds → clamped to first.distance
	assert.Equal(t, 0.0, interpolateBlockDistance(stops, 50))
}

func TestInterpolateBlockDistance_AfterLastStop(t *testing.T) {
	stops := []blockStopMetric{
		{EffectiveStopSeconds: 100, DistanceAlongBlock: 0},
		{EffectiveStopSeconds: 200, DistanceAlongBlock: 500},
	}
	// currentSeconds = 300 > last → clamped to last.distance
	assert.Equal(t, 500.0, interpolateBlockDistance(stops, 300))
}

func TestInterpolateBlockDistance_ExactStopTimes(t *testing.T) {
	stops := []blockStopMetric{
		{EffectiveStopSeconds: 100, DistanceAlongBlock: 0},
		{EffectiveStopSeconds: 200, DistanceAlongBlock: 500},
		{EffectiveStopSeconds: 300, DistanceAlongBlock: 1500},
	}
	// Exact-match boundaries — the loop's first hit returns from-stop's distance
	assert.Equal(t, 0.0, interpolateBlockDistance(stops, 100))
	assert.Equal(t, 500.0, interpolateBlockDistance(stops, 200))
	assert.Equal(t, 1500.0, interpolateBlockDistance(stops, 300))
}

func TestInterpolateBlockDistance_LinearMidpoint(t *testing.T) {
	stops := []blockStopMetric{
		{EffectiveStopSeconds: 100, DistanceAlongBlock: 0},
		{EffectiveStopSeconds: 200, DistanceAlongBlock: 1000},
	}
	// Halfway in time → halfway in distance
	assert.Equal(t, 500.0, interpolateBlockDistance(stops, 150))
	// Quarter-way
	assert.Equal(t, 250.0, interpolateBlockDistance(stops, 125))
}

func TestInterpolateBlockDistance_ZeroSpan(t *testing.T) {
	// Two stops with identical EffectiveStopSeconds; the function must avoid /0.
	stops := []blockStopMetric{
		{EffectiveStopSeconds: 100, DistanceAlongBlock: 250},
		{EffectiveStopSeconds: 100, DistanceAlongBlock: 250},
		{EffectiveStopSeconds: 200, DistanceAlongBlock: 1000},
	}
	// currentSeconds = 100 falls in the first interval [100, 100] (span 0).
	// Must not panic; returns the from-stop's distance.
	assert.Equal(t, 250.0, interpolateBlockDistance(stops, 100))
}

func TestPositionAndOrientationAtDistance_UnusableShape(t *testing.T) {
	pos, orient := positionAndOrientationAtDistance(nil, nil, 100)
	assert.Nil(t, pos)
	assert.Equal(t, -1.0, orient)

	// Mismatched lengths
	pos, orient = positionAndOrientationAtDistance(
		[]gtfs.ShapePoint{{Latitude: 1, Longitude: 1}},
		[]float64{0, 100},
		50,
	)
	assert.Nil(t, pos)
	assert.Equal(t, -1.0, orient)
}

func TestPositionAndOrientationAtDistance_ClampsBeforeStart(t *testing.T) {
	// East-pointing two-point shape ~111 km long (1° longitude at equator).
	shape := []gtfs.ShapePoint{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0, Longitude: 1},
	}
	cum := []float64{0, 100000}
	pos, orient := positionAndOrientationAtDistance(shape, cum, -10)
	require.NotNil(t, pos)
	assert.InDelta(t, 0.0, pos.Lat, 1e-9)
	assert.InDelta(t, 0.0, pos.Lon, 1e-9)
	// Heading East corresponds to 0° in OBA's convention (0=East, 90=North).
	assert.InDelta(t, 0.0, orient, 1e-6)
}

func TestPositionAndOrientationAtDistance_ClampsAfterEnd(t *testing.T) {
	shape := []gtfs.ShapePoint{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0, Longitude: 1},
	}
	cum := []float64{0, 100000}
	pos, _ := positionAndOrientationAtDistance(shape, cum, 999999)
	require.NotNil(t, pos)
	assert.InDelta(t, 0.0, pos.Lat, 1e-9)
	assert.InDelta(t, 1.0, pos.Lon, 1e-9)
}

func TestPositionAndOrientationAtDistance_Midsegment(t *testing.T) {
	// Two segments: (0,0) → (0,1) → (0,2)
	shape := []gtfs.ShapePoint{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0, Longitude: 1},
		{Latitude: 0, Longitude: 2},
	}
	cum := []float64{0, 100, 200}
	// distance 150 falls in segment 1 (100..200), 50% through → lon 1.5
	pos, orient := positionAndOrientationAtDistance(shape, cum, 150)
	require.NotNil(t, pos)
	assert.InDelta(t, 0.0, pos.Lat, 1e-9)
	assert.InDelta(t, 1.5, pos.Lon, 1e-9)
	// Still heading East
	assert.InDelta(t, 0.0, orient, 1e-6)
}

func TestPositionAndOrientationAtDistance_NorthSegment(t *testing.T) {
	// Pure-north segment: heading should be ≈ 90°.
	shape := []gtfs.ShapePoint{
		{Latitude: 0, Longitude: 0},
		{Latitude: 1, Longitude: 0},
	}
	cum := []float64{0, 111000}
	_, orient := positionAndOrientationAtDistance(shape, cum, 55500)
	assert.InDelta(t, 90.0, orient, 1e-6)
}

func TestMetricsForStop_TargetMissing(t *testing.T) {
	snap := &scheduledBlockSnapshot{
		Stops:     nil,
		StopIndex: map[scheduledStopKey]int{},
	}
	d, n, ok := snap.metricsForStop("trip-X", 5)
	assert.False(t, ok)
	assert.Equal(t, 0.0, d)
	assert.Equal(t, 0, n)
}

func TestMetricsForStop_SingleTripBlock(t *testing.T) {
	// 3-stop block, currentTime sits between stop 1 and stop 2.
	// Build by hand so we test ONLY the metricsForStop math.
	stops := []blockStopMetric{
		{TripID: "A", StopSequenceInTrip: 1, BlockSequence: 0, DistanceAlongBlock: 0, EffectiveStopSeconds: 100},
		{TripID: "A", StopSequenceInTrip: 2, BlockSequence: 1, DistanceAlongBlock: 500, EffectiveStopSeconds: 200},
		{TripID: "A", StopSequenceInTrip: 3, BlockSequence: 2, DistanceAlongBlock: 1500, EffectiveStopSeconds: 300},
	}
	snap := &scheduledBlockSnapshot{
		Stops:              stops,
		StopIndex:          buildStopIndex(stops),
		DistanceAlongBlock: 250, // halfway between stop 1 and stop 2
		NextStopIndex:      1,   // stop 2 (BlockSequence=1) is next
	}

	// Target = stop 1 (BlockSeq 0, dist 0): bus has just passed it.
	d, n, ok := snap.metricsForStop("A", 1)
	require.True(t, ok)
	assert.Equal(t, 0-250.0, d) // −250 m (past)
	assert.Equal(t, 0-1, n)     // −1 stop (past)

	// Target = stop 2 (BlockSeq 1, dist 500): bus is approaching.
	d, n, ok = snap.metricsForStop("A", 2)
	require.True(t, ok)
	assert.Equal(t, 250.0, d)
	assert.Equal(t, 0, n) // 1 − 1 = 0, already at next

	// Target = stop 3 (BlockSeq 2, dist 1500).
	d, n, ok = snap.metricsForStop("A", 3)
	require.True(t, ok)
	assert.Equal(t, 1250.0, d)
	assert.Equal(t, 1, n) // 2 − 1
}

func TestMetricsForStop_MultiTripBlock_NumberOfStopsAwayCanExceedTripLength(t *testing.T) {
	// Two trips in one block. Target stop on trip B; "next stop" still on trip A.
	// This is the case the Java response showed with numberOfStopsAway > trip's stop count.
	stops := []blockStopMetric{
		{TripID: "A", StopSequenceInTrip: 1, BlockSequence: 0, DistanceAlongBlock: 0, EffectiveStopSeconds: 100},
		{TripID: "A", StopSequenceInTrip: 2, BlockSequence: 1, DistanceAlongBlock: 1000, EffectiveStopSeconds: 200},
		{TripID: "B", StopSequenceInTrip: 1, BlockSequence: 2, DistanceAlongBlock: 2000, EffectiveStopSeconds: 300},
		{TripID: "B", StopSequenceInTrip: 2, BlockSequence: 3, DistanceAlongBlock: 3000, EffectiveStopSeconds: 400},
	}
	snap := &scheduledBlockSnapshot{
		Stops:              stops,
		StopIndex:          buildStopIndex(stops),
		DistanceAlongBlock: 200, // bus is early in trip A
		NextStopIndex:      1,   // next stop is A's stop 2 (BlockSeq 1)
	}

	// Target = B's stop 2 (BlockSeq 3). Bus is 2 block-stops away from next stop.
	d, n, ok := snap.metricsForStop("B", 2)
	require.True(t, ok)
	assert.Equal(t, 2800.0, d)
	assert.Equal(t, 2, n) // 3 − 1
}

func TestMetricsForStop_NextStopUnset(t *testing.T) {
	// currentTime is past every stop → NextStopIndex = -1. Mirroring Java's
	// behaviour, we report ok=false so the caller leaves distanceFromStop and
	// numberOfStopsAway at zero (rather than emitting a misleading negative
	// "you're N km past your stop" value for trips that ended hours ago).
	// Java reference: ScheduledBlockLocationServiceImpl returns null when
	// scheduleTime is past the block's last stop, which then causes
	// AbstractBlockLocationServiceImpl.getBlockLocation to return null and
	// applyBlockLocationToBean to skip the field assignment entirely.
	stops := []blockStopMetric{
		{TripID: "A", StopSequenceInTrip: 1, BlockSequence: 0, DistanceAlongBlock: 0, EffectiveStopSeconds: 100},
		{TripID: "A", StopSequenceInTrip: 2, BlockSequence: 1, DistanceAlongBlock: 500, EffectiveStopSeconds: 200},
	}
	snap := &scheduledBlockSnapshot{
		Stops:              stops,
		StopIndex:          buildStopIndex(stops),
		DistanceAlongBlock: 500,
		NextStopIndex:      -1,
	}

	d, n, ok := snap.metricsForStop("A", 1)
	assert.False(t, ok, "past-block-end should report no usable metrics")
	assert.Equal(t, 0.0, d)
	assert.Equal(t, 0, n)
}

func buildStopIndex(stops []blockStopMetric) map[scheduledStopKey]int {
	idx := make(map[scheduledStopKey]int, len(stops))
	for i, s := range stops {
		idx[scheduledStopKey{TripID: s.TripID, StopSequenceInTrip: s.StopSequenceInTrip}] = i
	}
	return idx
}

// Integration tests — driven against the RABA fixture loaded by createTestApi.

// rabaServiceDate is a Monday in the RABA dataset's active service period,
// same value other tests in this package use.
var rabaServiceDate = time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC)

// findTripWithBlockAndShape returns the ID of the first RABA trip that has
// both a block_id and a shape_id — every DB-touching test needs both.
func findTripWithBlockAndShape(t testing.TB, api *RestAPI) string {
	t.Helper()
	var tripID string
	err := api.GtfsManager.GtfsDB.DB.QueryRowContext(context.Background(),
		`SELECT id FROM trips
		 WHERE block_id IS NOT NULL AND block_id != ''
		   AND shape_id IS NOT NULL AND shape_id != ''
		 LIMIT 1`,
	).Scan(&tripID)
	require.NoError(t, err, "RABA fixture should contain at least one trip with both block_id and shape_id")
	return tripID
}

func TestBlockTripIDsForServiceDate_ReturnsBlockMembers(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	ids := api.blockTripIDsForServiceDate(ctx, tripID, rabaServiceDate)
	assert.NotEmpty(t, ids)
	assert.Contains(t, ids, tripID, "target trip should be in its own block's member list")
}

func TestBlockTripIDsForServiceDate_FallsBackForUnknownTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	const unknown = "trip-that-does-not-exist"
	ids := api.blockTripIDsForServiceDate(ctx, unknown, rabaServiceDate)
	assert.Equal(t, []string{unknown}, ids,
		"unknown trip should fall back to a one-trip block containing itself")
}

func TestLoadBlockTripData_PopulatesStopTimesAndShape(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)
	data := api.loadBlockTripData(ctx, []string{tripID})

	require.Len(t, data, 1)
	d := data[0]
	assert.Equal(t, tripID, d.id)
	assert.NotEmpty(t, d.stopTimes, "trip should have stop times")
	assert.NotEmpty(t, d.shapePoints, "trip should have shape points")
	assert.Greater(t, d.totalDist, 0.0, "shape total distance should be > 0")
	assert.Len(t, d.cumDistances, len(d.shapePoints), "cum distances should align with shape points")
	assert.LessOrEqual(t, d.firstSeconds, d.lastSeconds, "first stop must not be after last stop")
}

func TestLoadBlockTripData_SkipsTripsWithNoData(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	data := api.loadBlockTripData(ctx, []string{"trip-does-not-exist"})
	assert.Empty(t, data)
}

func TestFetchStopCoordsForStopTimes_DedupesAndReturnsMap(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes)

	coords := api.fetchStopCoordsForStopTimes(ctx, stopTimes)
	require.NotNil(t, coords)
	for _, st := range stopTimes {
		stop, ok := coords[st.StopID]
		assert.True(t, ok, "stop %q should be in coord map", st.StopID)
		assert.NotEqual(t, 0.0, stop.Lat, "stop lat should be non-zero")
		assert.NotEqual(t, 0.0, stop.Lon, "stop lon should be non-zero")
	}
}

func TestFetchStopCoordsForStopTimes_EmptyInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	coords := api.fetchStopCoordsForStopTimes(context.Background(), nil)
	assert.Nil(t, coords)
}

func TestComputeScheduledBlockSnapshot_BasicShape(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	// Pick a currentTime in the middle of the service day so we land inside the trip.
	currentTime := rabaServiceDate.Add(12 * time.Hour)
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, currentTime, rabaServiceDate)
	require.NotNil(t, snap, "snapshot should be built for a real RABA trip")

	assert.NotEmpty(t, snap.Stops)
	assert.Equal(t, len(snap.Stops), len(snap.StopIndex), "StopIndex must cover every stop")

	// Stops slice is ordered by block sequence == position in slice
	for i, s := range snap.Stops {
		assert.Equal(t, i, s.BlockSequence, "BlockSequence should match slice position")
	}

	// Cumulative DistanceAlongBlock is monotonically non-decreasing.
	for i := 1; i < len(snap.Stops); i++ {
		assert.GreaterOrEqual(t, snap.Stops[i].DistanceAlongBlock, snap.Stops[i-1].DistanceAlongBlock,
			"DistanceAlongBlock must be monotonic (stop %d vs %d)", i-1, i)
	}
}

func TestComputeScheduledBlockSnapshot_ClampedBeforeBlockStarts(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	// 1 minute past midnight on serviceDate → before every block starts.
	currentTime := rabaServiceDate.Add(1 * time.Minute)
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, currentTime, rabaServiceDate)
	require.NotNil(t, snap)

	// Block hasn't started — DistanceAlongBlock clamps to first stop's distance,
	// no active trip is selected.
	assert.Equal(t, snap.Stops[0].DistanceAlongBlock, snap.DistanceAlongBlock)
	assert.Empty(t, snap.ActiveTripID, "no active trip when currentTime precedes the block")
	// NextStopIndex should point at the first stop.
	assert.GreaterOrEqual(t, snap.NextStopIndex, 0)
}

func TestComputeScheduledBlockSnapshot_ClampedAfterBlockEnds(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	// 1 day after the service date — way past every block.
	currentTime := rabaServiceDate.Add(48 * time.Hour)
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, currentTime, rabaServiceDate)
	require.NotNil(t, snap)

	// All stops are in the past — NextStopIndex must be -1.
	assert.Equal(t, -1, snap.NextStopIndex)
	// DistanceAlongBlock clamps to the last stop.
	assert.Equal(t, snap.Stops[len(snap.Stops)-1].DistanceAlongBlock, snap.DistanceAlongBlock)
	// ActiveTripID is set to the latest trip (the block's last trip).
	assert.NotEmpty(t, snap.ActiveTripID, "after-block currentTime still picks an active trip")
}

func TestComputeScheduledBlockSnapshot_MetricsAtKnownTimes(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	// Build a snapshot in the middle of the service day, then use metricsForStop
	// to verify the documented sign convention.
	currentTime := rabaServiceDate.Add(12 * time.Hour)
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, currentTime, rabaServiceDate)
	require.NotNil(t, snap)

	// Pick the very first stop on this trip; with currentTime at 12:00 the bus
	// has long since passed it, so distanceFromStop and numberOfStopsAway must
	// be ≤ 0.
	firstStop := snap.Stops[0]
	require.Equal(t, tripID, firstStop.TripID, "first stop of Stops should belong to the earliest block trip")

	d, n, ok := snap.metricsForStop(firstStop.TripID, firstStop.StopSequenceInTrip)
	require.True(t, ok)
	assert.LessOrEqual(t, d, 0.0, "first block stop must be behind the bus at noon")
	assert.LessOrEqual(t, n, 0, "first block stop must have numberOfStopsAway ≤ 0 at noon")
}

// TestComputeScheduledBlockSnapshot_TripStartBoundary pins Java's semantic
// (TripStatusBeanServiceImpl:283-292): ActiveTripScheduledDistance is the
// vehicle's position WITHIN the snapshot's active trip — the trip the
// vehicle is currently operating — and MUST be less than that active
// trip's total distance. Otherwise the response emits a value inconsistent
// with which trip it labels as "active" (see
// docs/scheduled_distance_boundary_bug.md).
//
// The failure mode this test catches: when the queried trip is a FUTURE
// trip in a multi-trip block and currentSeconds is before that trip
// starts, the snapshot should either (a) pick the currently-running
// earlier trip as active and report a position within its length, or
// (b) leave ActiveTripID empty. It must NOT report a value that exceeds
// ActiveTripTotalDistance, which would mean the offset math combined
// distances from different trips.
func TestComputeScheduledBlockSnapshot_TripStartBoundary(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Find a block with ≥3 trips so we can pick a middle one (avoids the
	// "first trip in block" edge case where activeTripOffset = 0 legitimately).
	var blockID string
	err := api.GtfsManager.GtfsDB.DB.QueryRowContext(ctx,
		`SELECT block_id FROM trips
		 WHERE block_id IS NOT NULL AND block_id != ''
		   AND shape_id IS NOT NULL AND shape_id != ''
		 GROUP BY block_id HAVING COUNT(*) >= 3
		 LIMIT 1`,
	).Scan(&blockID)
	require.NoError(t, err, "RABA should have a block with 3+ trips")

	rows, err := api.GtfsManager.GtfsDB.DB.QueryContext(ctx,
		`SELECT t.id, MIN(st.arrival_time) AS first_ns
		 FROM trips t
		 JOIN stop_times st ON st.trip_id = t.id
		 WHERE t.block_id = ?
		 GROUP BY t.id
		 ORDER BY first_ns`,
		blockID,
	)
	require.NoError(t, err)
	type tripRow struct {
		id       string
		firstSec int64
	}
	var trips []tripRow
	for rows.Next() {
		var r tripRow
		var firstNs int64
		require.NoError(t, rows.Scan(&r.id, &firstNs))
		r.firstSec = firstNs / int64(time.Second)
		trips = append(trips, r)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.GreaterOrEqual(t, len(trips), 3)

	// Target the second trip so it's neither first nor last.
	target := trips[1]
	t.Logf("block=%s target=%s firstSec=%d", blockID, target.id, target.firstSec)

	for _, offset := range []int64{-60, 0, 60} {
		currentTime := rabaServiceDate.Add(time.Duration(target.firstSec+offset) * time.Second)
		snap := api.computeScheduledBlockSnapshot(ctx, target.id, currentTime, rabaServiceDate)
		require.NotNil(t, snap, "snapshot must be non-nil at offset %+d", offset)

		if snap.ActiveTripID == "" {
			continue // Java's null-BlockLocation equivalent; downstream skips.
		}

		// The core invariant: the position within the active trip must be
		// less than that trip's total. Anything else means offset math
		// combined distances across different trips and the response would
		// emit values inconsistent with the labelled ActiveTripID.
		assert.LessOrEqual(t, snap.ActiveTripScheduledDistance, snap.ActiveTripTotalDistance+50.0,
			"offset %+ds: ActiveTripScheduledDistance=%.1f exceeds ActiveTripTotalDistance=%.1f — offset math combined trips",
			offset, snap.ActiveTripScheduledDistance, snap.ActiveTripTotalDistance)
	}
}

func TestProjectStopsInSequence_MonotonicAlongShape(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)
	data := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, data, 1)
	td := data[0]
	require.GreaterOrEqual(t, len(td.stopTimes), 2)

	stopByID := api.fetchStopCoordsForStopTimes(ctx, td.stopTimes)
	require.NotNil(t, stopByID)

	distances := projectStopsInSequence(td.stopTimes, stopByID, td.shapePoints, td.cumDistances)
	require.Len(t, distances, len(td.stopTimes))

	// Monotonicity is the key invariant — without it loop routes break.
	for i := 1; i < len(distances); i++ {
		assert.GreaterOrEqual(t, distances[i], distances[i-1],
			"distances must be monotonic in stop_sequence order (stop %d vs %d)", i-1, i)
	}
	assert.GreaterOrEqual(t, distances[0], 0.0)
	assert.LessOrEqual(t, distances[len(distances)-1], td.totalDist+1.0)
}

func TestProjectStopsInSequence_GracefulOnEmptyShape(t *testing.T) {
	// Unusable shape inputs must return a zeroed slice of the right length,
	// not panic. (The "unknown stop" case is no longer a useful test because
	// the function now prefers shape_dist_traveled — when the publisher
	// provides it the function returns scaled distances regardless of
	// whether the stop coords were resolved.)
	stopTimes := []gtfsdb.StopTime{{StopID: "x"}, {StopID: "y"}}
	distances := projectStopsInSequence(stopTimes, nil, nil, nil)
	require.Len(t, distances, 2)
	assert.Equal(t, 0.0, distances[0])
	assert.Equal(t, 0.0, distances[1])
}

// TestProjectStopsInSequence_ProjectsAllStopsGeometrically pins the
// Java-parity port: projectStopsInSequence must IGNORE the publisher's
// shape_dist_traveled entirely and always project each stop's lat/lon
// onto the shape polyline. Java's StopTimeEntriesFactory.
// ensureStopTimesHaveShapeDistanceTraveledSet overwrites the feed value
// with the projection at load time; we do the same at query time so the
// emitted distances are always metres regardless of what unit the
// publisher chose (km, miles, feet, unitless).
func TestProjectStopsInSequence_ProjectsAllStopsGeometrically(t *testing.T) {
	// Straight-line shape, 5 evenly spaced points across longitude 0..0.001.
	// utils.Distance per segment ~27.8m → cumulative ~0, 27.8, 55.6, 83.4, 111.2.
	shape := []gtfs.ShapePoint{
		{Latitude: 0.0, Longitude: 0.0},
		{Latitude: 0.0, Longitude: 0.00025},
		{Latitude: 0.0, Longitude: 0.00050},
		{Latitude: 0.0, Longitude: 0.00075},
		{Latitude: 0.0, Longitude: 0.00100},
	}
	cumDist := preCalculateCumulativeDistances(shape)
	totalDist := cumDist[len(cumDist)-1]

	// Feed publishes bogus shape_dist_traveled values (arbitrary unit,
	// nowhere near metres). Java's ensureStopTimesHaveShapeDistanceTraveledSet
	// overwrites these with projection-derived metres at load time; we do the
	// same at query time — the publisher's ShapeDistTraveled must be IGNORED
	// entirely and the stop's projected geometric distance used instead.
	stopTimes := []gtfsdb.StopTime{
		{StopID: "stop_A", StopSequence: 1, ShapeDistTraveled: sql.NullFloat64{Float64: 5000, Valid: true}},
		{StopID: "stop_B", StopSequence: 2, ShapeDistTraveled: sql.NullFloat64{Float64: 5000, Valid: true}},
		{StopID: "stop_C", StopSequence: 3, ShapeDistTraveled: sql.NullFloat64{Float64: 5000, Valid: true}},
	}
	stopByID := map[string]gtfsdb.Stop{
		"stop_A": {ID: "stop_A", Lat: 0.0, Lon: 0.0000}, // shape start
		"stop_B": {ID: "stop_B", Lat: 0.0, Lon: 0.0005}, // midpoint
		"stop_C": {ID: "stop_C", Lat: 0.0, Lon: 0.0010}, // shape end
	}

	distances := projectStopsInSequence(stopTimes, stopByID, shape, cumDist)
	require.Len(t, distances, 3)

	// Distances reflect geometry, NOT the bogus publisher-unit values.
	assert.InDelta(t, 0.0, distances[0], 1.0,
		"stop at shape start projects to ~0m regardless of ShapeDistTraveled")
	assert.InDelta(t, totalDist/2, distances[1], 1.0,
		"stop at shape midpoint projects to ~totalDist/2 regardless of ShapeDistTraveled")
	assert.InDelta(t, totalDist, distances[2], 1.0,
		"stop at shape end projects to ~totalDist regardless of ShapeDistTraveled")

	for i := 1; i < len(distances); i++ {
		assert.GreaterOrEqual(t, distances[i], distances[i-1],
			"distances must be monotonic in stop_sequence order (stop %d vs %d)", i-1, i)
	}
}

// TestProjectStopsInSequence_LoopRouteRevisitsSameCoords pins the whole
// reason projectStopsInSequence exists: when the same (lat, lon) appears
// at two different shape segments (figure-eight / lasso / Q-route loop),
// a naive global-minimum projection picks the SAME segment for both
// stops, producing equal distances and ultimately the catastrophic
// distanceFromStop outliers documented at scheduled_block_helper.go:388.
//
// We build a figure-eight shape that revisits (0, 0) at the midpoint:
// stop_A at sequence 1 is at the start (shape segment 0); stop_B at
// sequence 4 is at the same (0, 0) coords but should project to the
// LATER segment near point[3]. With the monotonic cursor advancing,
// distances[3] must be strictly greater than distances[0] — both
// physical coordinates are identical, only the cursor tells them apart.
func TestProjectStopsInSequence_LoopRouteRevisitsSameCoords(t *testing.T) {
	// Figure-eight shape that passes through (0, 0) twice:
	//   point 0: (0, 0)       — start, where stop_A lives
	//   point 1: (0.0001, 0)  — far point of first lobe
	//   point 2: (0, 0)       — back at origin (midpoint crossing)
	//   point 3: (0, 0.0001)  — far point of second lobe
	//   point 4: (0, 0)       — close the loop at origin
	shape := []gtfs.ShapePoint{
		{Latitude: 0.0, Longitude: 0.0},
		{Latitude: 0.0001, Longitude: 0.0},
		{Latitude: 0.0, Longitude: 0.0},
		{Latitude: 0.0, Longitude: 0.0001},
		{Latitude: 0.0, Longitude: 0.0},
	}
	cumDist := preCalculateCumulativeDistances(shape)
	require.Len(t, cumDist, 5)
	require.Greater(t, cumDist[len(cumDist)-1], 0.0, "shape must have non-zero total distance")

	// Four stops in sequence:
	//   stop_A (seq 1, coords (0,0))     — at point 0
	//   stop_B (seq 2, coords (0.0001,0))— at point 1
	//   stop_C (seq 3, coords (0,0))     — at point 2 / midpoint crossing (SAME COORDS as stop_A!)
	//   stop_D (seq 4, coords (0,0.0001))— at point 3
	stopTimes := []gtfsdb.StopTime{
		{StopID: "stop_A", StopSequence: 1},
		{StopID: "stop_B", StopSequence: 2},
		{StopID: "stop_C", StopSequence: 3},
		{StopID: "stop_D", StopSequence: 4},
	}
	stopByID := map[string]gtfsdb.Stop{
		"stop_A": {ID: "stop_A", Lat: 0.0, Lon: 0.0},
		"stop_B": {ID: "stop_B", Lat: 0.0001, Lon: 0.0},
		"stop_C": {ID: "stop_C", Lat: 0.0, Lon: 0.0},
		"stop_D": {ID: "stop_D", Lat: 0.0, Lon: 0.0001},
	}

	distances := projectStopsInSequence(stopTimes, stopByID, shape, cumDist)
	require.Len(t, distances, 4)

	// Monotonicity is THE invariant. If lastMatchedIndex didn't advance
	// through the figure-eight, stop_C (same coords as stop_A) would
	// project back to segment 0 and distances[2] < distances[1].
	for i := 1; i < len(distances); i++ {
		assert.GreaterOrEqual(t, distances[i], distances[i-1],
			"loop-route monotonicity: distances[%d]=%.2f must be ≥ distances[%d]=%.2f even when coords repeat",
			i, distances[i], i-1, distances[i-1])
	}
	// stop_C revisits stop_A's exact coords but is at sequence 3 — it
	// MUST project to a later shape distance, not back to 0.
	assert.Greater(t, distances[2], distances[0],
		"stop_C shares coords with stop_A but appears later in sequence; "+
			"must project to a later shape segment, not the first one")
}

func TestApplyScheduledTripPositionToStatus_PopulatesFields(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)
	data := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, data, 1)
	td := data[0]

	status := models.NewTripStatus()
	// currentTime well into the trip so we get a non-zero scheduled distance.
	currentTime := rabaServiceDate.Add(12 * time.Hour)
	api.applyScheduledTripPositionToStatus(
		ctx, status, td.stopTimes, td.shapePoints, td.cumDistances, currentTime, rabaServiceDate,
	)

	assert.GreaterOrEqual(t, status.ScheduledDistanceAlongTrip, 0.0)
	assert.LessOrEqual(t, status.ScheduledDistanceAlongTrip, td.totalDist+1.0)
	assert.NotEqual(t, models.Location{}, status.Position, "Position must be projected onto the shape, not (0, 0)")
}

func TestApplyScheduledTripPositionToStatus_NoOpOnEmptyInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	status := models.NewTripStatus()
	api.applyScheduledTripPositionToStatus(
		context.Background(), status, nil, nil, nil, time.Now(), rabaServiceDate,
	)
	assert.Equal(t, 0.0, status.ScheduledDistanceAlongTrip)
	assert.Equal(t, models.Location{}, status.Position)
}

// ---------------------------------------------------------------------------
// keepShiftContainingTrip — pure-function unit tests.
//
// Before this PR, the shift-splitting logic was only exercised indirectly via
// computeScheduledBlockSnapshot against RABA, which has no overlapping trips.
// The function exists specifically to handle feeds that reuse one block_id
// across multiple physical buses (overlapping trip windows), so the test
// scenarios MUST include actual overlaps to be meaningful.
// ---------------------------------------------------------------------------

func shiftTrip(id string, first, last int64) blockTripData {
	return blockTripData{id: id, firstSeconds: first, lastSeconds: last}
}

func shiftIDs(trips []blockTripData) []string {
	ids := make([]string, len(trips))
	for i, t := range trips {
		ids[i] = t.id
	}
	return ids
}

func TestKeepShiftContainingTrip_EmptyInputReturnsNil(t *testing.T) {
	assert.Nil(t, keepShiftContainingTrip(nil, "target"))
	assert.Nil(t, keepShiftContainingTrip([]blockTripData{}, "target"))
}

func TestKeepShiftContainingTrip_TargetNotInBlockReturnsNil(t *testing.T) {
	trips := []blockTripData{
		shiftTrip("A", 100, 200),
		shiftTrip("B", 300, 400),
	}
	assert.Nil(t, keepShiftContainingTrip(trips, "C"),
		"target not in block must return nil so callers fall back gracefully")
}

func TestKeepShiftContainingTrip_NoOverlapsKeepsEntireBlock(t *testing.T) {
	// Three sequential, non-overlapping trips. Target is the middle one.
	trips := []blockTripData{
		shiftTrip("morning", 100, 200),
		shiftTrip("midday", 300, 400),
		shiftTrip("evening", 500, 600),
	}
	got := keepShiftContainingTrip(trips, "midday")
	assert.Equal(t, []string{"morning", "midday", "evening"}, shiftIDs(got),
		"no overlaps anywhere → no split; whole block is one shift")
}

func TestKeepShiftContainingTrip_OverlapBeforeTargetCutsStart(t *testing.T) {
	// "earlier" ends at 250; "target" starts at 200 — overlap! That means a
	// different physical bus is running "target" and earlier trips belong
	// to a different shift. Cut start at "target".
	trips := []blockTripData{
		shiftTrip("earlier", 100, 250),
		shiftTrip("target", 200, 300),
		shiftTrip("later", 350, 450),
	}
	got := keepShiftContainingTrip(trips, "target")
	assert.Equal(t, []string{"target", "later"}, shiftIDs(got),
		"overlap before target must cut start; 'earlier' belongs to another shift")
}

func TestKeepShiftContainingTrip_OverlapAfterTargetCutsEnd(t *testing.T) {
	trips := []blockTripData{
		shiftTrip("earlier", 100, 150),
		shiftTrip("target", 200, 350),
		shiftTrip("overlapping_next", 300, 400), // starts before target ends
	}
	got := keepShiftContainingTrip(trips, "target")
	assert.Equal(t, []string{"earlier", "target"}, shiftIDs(got),
		"overlap after target must cut end; 'overlapping_next' belongs to another shift")
}

func TestKeepShiftContainingTrip_ThreeShiftBlockTargetInMiddle(t *testing.T) {
	// Three distinct shifts. Target is in the middle one (B-shift).
	//   shift A: A1 → A2  (overlap with B1 starts here)
	//   shift B: B1 → B2  ← target=B1
	//   shift C: C1 → C2
	trips := []blockTripData{
		shiftTrip("A1", 100, 250),
		shiftTrip("A2", 200, 300),
		shiftTrip("B1", 280, 380), // target — starts before A2 ends → overlap → cut
		shiftTrip("B2", 400, 500),
		shiftTrip("C1", 480, 580), // starts before B2 ends → overlap → cut
		shiftTrip("C2", 600, 700),
	}
	got := keepShiftContainingTrip(trips, "B1")
	assert.Equal(t, []string{"B1", "B2"}, shiftIDs(got),
		"three-shift block must isolate the B-shift; A and C belong to other physical buses")
}

func TestKeepShiftContainingTrip_TargetIsFirstTrip(t *testing.T) {
	trips := []blockTripData{
		shiftTrip("target", 100, 200),
		shiftTrip("overlapping_next", 150, 300),
		shiftTrip("after", 350, 450),
	}
	got := keepShiftContainingTrip(trips, "target")
	assert.Equal(t, []string{"target"}, shiftIDs(got),
		"target at start with overlapping next → just the target")
}

func TestKeepShiftContainingTrip_TargetIsLastTrip(t *testing.T) {
	// "first" and "second" overlap → shift boundary between them.
	// "target" doesn't overlap with "second" → stays in the same shift.
	trips := []blockTripData{
		shiftTrip("first", 100, 200),
		shiftTrip("second", 150, 300), // overlaps with "first"
		shiftTrip("target", 400, 500),
	}
	got := keepShiftContainingTrip(trips, "target")
	assert.Equal(t, []string{"second", "target"}, shiftIDs(got),
		"shift cut happens at the overlap between 'first' and 'second'; "+
			"the rest of the chain (second, target) is target's shift")
}

func TestKeepShiftContainingTrip_BackToBackTouchingNoOverlap(t *testing.T) {
	// trips[i].firstSeconds == trips[i-1].lastSeconds is NOT an overlap
	// (one bus arrives just as another departs is fine). The function's
	// test is strict less-than: `trips[i].firstSeconds < trips[i-1].lastSeconds`.
	trips := []blockTripData{
		shiftTrip("A", 100, 200),
		shiftTrip("B", 200, 300), // exactly back-to-back
		shiftTrip("C", 300, 400),
	}
	got := keepShiftContainingTrip(trips, "B")
	assert.Equal(t, []string{"A", "B", "C"}, shiftIDs(got),
		"touching boundaries (firstSeconds == prev lastSeconds) are not overlaps")
}
