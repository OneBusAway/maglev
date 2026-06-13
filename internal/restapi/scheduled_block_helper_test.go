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

// TestProjectStopsInSequence_MixedAuthoritativeAndGeometric exercises the
// mixed-mode path: one stop uses shape_dist_traveled (authoritative), the
// next falls back to geometric projection. Validates that:
//
//  1. authoritative distances are returned verbatim (scale=1 when max==total),
//  2. the geometric stop's projection produces a monotonic non-decreasing
//     distance, which requires lastMatchedIndex to advance through the shape
//     after each authoritative match.
func TestProjectStopsInSequence_MixedAuthoritativeAndGeometric(t *testing.T) {
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

	// Three stops: A (authoritative, start), B (geometric, middle),
	// C (authoritative, end). max == totalDist ⇒ shapeDistScale = 1,
	// so the authoritative values are emitted verbatim and we can assert
	// magnitudes directly.
	stopTimes := []gtfsdb.StopTime{
		{StopID: "stop_A", StopSequence: 1, ShapeDistTraveled: sql.NullFloat64{Float64: 11.1, Valid: true}},
		{StopID: "stop_B", StopSequence: 2},
		{StopID: "stop_C", StopSequence: 3, ShapeDistTraveled: sql.NullFloat64{Float64: totalDist, Valid: true}},
	}
	stopByID := map[string]gtfsdb.Stop{
		"stop_B": {ID: "stop_B", Lat: 0.0, Lon: 0.0007}, // ~78m along the shape
	}

	distances := projectStopsInSequence(stopTimes, stopByID, shape, cumDist)
	require.Len(t, distances, 3)
	assert.Equal(t, 11.1, distances[0], "authoritative stop uses shape_dist_traveled verbatim when scale==1")
	assert.Equal(t, totalDist, distances[2], "trailing authoritative stop equals the shape's total distance")

	// Monotonicity: the geometric stop must land between its two
	// authoritative neighbours, never regress before the previous match.
	for i := 1; i < len(distances); i++ {
		assert.GreaterOrEqual(t, distances[i], distances[i-1],
			"distances must be monotonic in stop_sequence order (stop %d vs %d)", i-1, i)
	}
	assert.LessOrEqual(t, distances[1], totalDist, "geometric stop must stay within shape bounds")
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
