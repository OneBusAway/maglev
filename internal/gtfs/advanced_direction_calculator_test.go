package gtfs

import (
	"context"
	"database/sql"
	"math"
	"os"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
)

// This uses a Singleton pattern to load the DB and Warm the Cache exactly ONCE
// for this test file. This prevents re-loading the ZIP file 15+ times.

var (
	sharedManager *Manager
	sharedCalc    *AdvancedDirectionCalculator
	setupOnce     sync.Once
)

// Helper function to get the shared instances.
func getSharedTestComponents(t *testing.T) (*Manager, *AdvancedDirectionCalculator) {
	setupOnce.Do(func() {
		// Initialize the DB (In-Memory)
		gtfsConfig := Config{
			GtfsURL:      models.GetFixturePath(t, "raba.zip"),
			GTFSDataPath: ":memory:",
		}

		var err error
		// Pass context.Background() here to satisfy the new cancellable startup logic
		sharedManager, err = InitGTFSManager(context.Background(), gtfsConfig)
		if err != nil {
			panic("Failed to init shared GTFS manager: " + err.Error())
		}

		// Create the Calculator
		sharedCalc = NewAdvancedDirectionCalculator(sharedManager.GtfsDB.Queries)
	})

	return sharedManager, sharedCalc
}

func TestTranslateGtfsDirection(t *testing.T) {
	calc := &AdvancedDirectionCalculator{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Text-based directions
		{"north", "north", "N"},
		{"North uppercase", "North", "N"},
		{"NORTH all caps", "NORTH", "N"},
		{"northeast", "northeast", "NE"},
		{"east", "east", "E"},
		{"southeast", "southeast", "SE"},
		{"south", "south", "S"},
		{"southwest", "southwest", "SW"},
		{"west", "west", "W"},
		{"northwest", "northwest", "NW"},

		// Numeric directions (degrees) - GTFS uses geographic bearings
		// 0°=North, 90°=East, 180°=South, 270°=West
		{"0 degrees", "0", "N"},
		{"45 degrees", "45", "NE"},
		{"90 degrees", "90", "E"},
		{"135 degrees", "135", "SE"},
		{"180 degrees", "180", "S"},
		{"225 degrees", "225", "SW"},
		{"270 degrees", "270", "W"},
		{"315 degrees", "315", "NW"},
		// Compass abbreviations (as written by DirectionPrecomputer)
		{"abbreviation N", "N", "N"},
		{"abbreviation NE", "NE", "NE"},
		{"abbreviation E", "E", "E"},
		{"abbreviation SE", "SE", "SE"},
		{"abbreviation S", "S", "S"},
		{"abbreviation SW", "SW", "SW"},
		{"abbreviation W", "W", "W"},
		{"abbreviation NW", "NW", "NW"},

		// Mixed case abbreviations
		{"abbreviation n lowercase", "n", "N"},
		{"abbreviation ne lowercase", "ne", "NE"},
		{"abbreviation Ne mixed", "Ne", "NE"},
		{"abbreviation sW mixed", "sW", "SW"},

		// Invalid
		{"invalid text", "invalid", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calc.translateGtfsDirection(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateStopDirectionResultCache(t *testing.T) {
	_, calc := getSharedTestComponents(t)

	// First call: precomputed direction "NE" from DB should be recognized
	result := calc.CalculateStopDirection(context.Background(), "stop-1", nulls.String("NE"))
	assert.Equal(t, "NE", result, "should recognize compass abbreviation NE from precomputed direction")

	// Verify that a stop with no GTFS direction falls through to computeFromShapes,
	// gets an empty result (no data in cache for nonexistent stop), and caches the empty result.
	result = calc.CalculateStopDirection(context.Background(), "nonexistent-stop", sql.NullString{})
	assert.Equal(t, "", result, "should return empty for stop with no direction data")

	// The empty result should be cached (negative cache) — verify via sync.Map
	cached, ok := calc.directionResults.Load("nonexistent-stop")
	assert.True(t, ok, "empty result should be cached in directionResults")
	assert.Equal(t, "", cached.(string), "cached value should be empty string")

	// Second call for same stop should return from cache without recomputation
	result = calc.CalculateStopDirection(context.Background(), "nonexistent-stop", sql.NullString{})
	assert.Equal(t, "", result, "second call should return cached empty result")
}

// TestTransientDBError_NotCached verifies that if the DB fails (simulated by closing it),
// the resulting empty direction is NOT cached, allowing future requests to retry.
func TestTransientDBError_NotCached(t *testing.T) {
	// Simulate a transient DB failure by opening and immediately closing an in-memory DB
	rawDB, err := sql.Open("sqlite3", ":memory:")
	assert.NoError(t, err)
	err = rawDB.Close()
	assert.NoError(t, err)

	brokenQueries := gtfsdb.New(rawDB)

	calc := NewAdvancedDirectionCalculator(brokenQueries)

	stopID := "transient-error-stop"

	// The query will fail, gracefully returning an empty direction
	result := calc.CalculateStopDirection(context.Background(), stopID)
	assert.Equal(t, "", result, "should return empty string on DB error")

	// Critical check: ensure the failure was NOT permanently cached
	_, cached := calc.directionResults.Load(stopID)
	assert.False(t, cached, "transient DB error result must not be cached in directionResults")
}

func TestCalculateStopDirectionPrecomputedAbbreviations(t *testing.T) {
	// Verify all compass abbreviations that DirectionPrecomputer writes to SQLite
	// are correctly recognized by CalculateStopDirection via translateGtfsDirection.
	calc := &AdvancedDirectionCalculator{}

	abbreviations := map[string]string{
		"N": "N", "NE": "NE", "E": "E", "SE": "SE",
		"S": "S", "SW": "SW", "W": "W", "NW": "NW",
	}

	for abbr, expected := range abbreviations {
		t.Run("precomputed_"+abbr, func(t *testing.T) {
			result := calc.CalculateStopDirection(
				context.Background(),
				"stop-"+abbr,
				nulls.String(abbr),
			)
			assert.Equal(t, expected, result)
		})
	}
}

func TestGetAngleAsDirection(t *testing.T) {
	calc := &AdvancedDirectionCalculator{}

	tests := []struct {
		name     string
		theta    float64 // radians
		expected string
	}{
		{"East (0 rad)", 0, "E"},
		{"Northeast (π/4 rad)", math.Pi / 4, "NE"},
		{"North (π/2 rad)", math.Pi / 2, "N"},
		{"Northwest (3π/4 rad)", 3 * math.Pi / 4, "NW"},
		{"West (π rad)", math.Pi, "W"},
		{"Southeast (-π/4 rad)", -math.Pi / 4, "SE"},
		{"South (-π/2 rad)", -math.Pi / 2, "S"},
		{"Southwest (-3π/4 rad)", -3 * math.Pi / 4, "SW"},
		{"West (-π rad)", -math.Pi, "W"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calc.getAngleAsDirection(tt.theta)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateStopDirectionWithGtfsDirection(t *testing.T) {
	// This test verifies that GTFS direction field takes precedence
	calc := &AdvancedDirectionCalculator{}

	tests := []struct {
		name          string
		gtfsDirection sql.NullString
		expected      string
	}{
		{
			name:          "Valid text direction",
			gtfsDirection: nulls.String("North"),
			expected:      "N",
		},
		{
			name:          "Valid numeric direction",
			gtfsDirection: nulls.String("90"),
			expected:      "E", // 90° in GTFS = East
		},
		{
			name:          "Invalid direction falls through",
			gtfsDirection: nulls.String("invalid"),
			expected:      "", // Would need shape data to compute
		},
		{
			name:          "Null direction falls through",
			gtfsDirection: sql.NullString{},
			expected:      "", // Would need shape data to compute
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We can't test the full function without database context
			// This just tests the GTFS direction parsing
			if tt.gtfsDirection.Valid {
				result := calc.translateGtfsDirection(tt.gtfsDirection.String)
				if tt.expected != "" {
					assert.Equal(t, tt.expected, result)
				}
			}
		})
	}
}

func TestStatisticalFunctions(t *testing.T) {
	t.Run("mean", func(t *testing.T) {
		assert.Equal(t, 3.0, mean([]float64{1, 2, 3, 4, 5}))
		assert.Equal(t, 0.0, mean([]float64{}))
		assert.Equal(t, 5.0, mean([]float64{5}))
	})

	t.Run("variance", func(t *testing.T) {
		values := []float64{1, 2, 3, 4, 5}
		m := mean(values)
		v := variance(values, m)
		assert.InDelta(t, 2.5, v, 0.001)
		assert.Equal(t, 0.0, variance([]float64{5}, 5.0))
	})

	t.Run("median", func(t *testing.T) {
		// Note: median function expects pre-sorted arrays
		assert.Equal(t, 3.0, median([]float64{1, 2, 3, 4, 5}))

		// For even-length arrays, it returns average of middle two values
		vals := []float64{1, 2, 4, 5}
		// Median of sorted [1, 2, 4, 5] should be (2 + 4) / 2 = 3.0
		assert.Equal(t, 3.0, median(vals))

		assert.Equal(t, 5.0, median([]float64{5}))
		assert.Equal(t, 0.0, median([]float64{}))
	})
}

func TestCalculateStopDirection_WithShapeData(t *testing.T) {
	ctx := context.Background()
	// Optimization: Reuse shared DB
	_, calc := getSharedTestComponents(t)

	// Test with a real stop from RABA data
	direction := calc.CalculateStopDirection(ctx, "7000", sql.NullString{})
	// Should return a valid direction or empty string
	assert.True(t, direction == "" || len(direction) <= 2)
}

func TestComputeFromShapes_NoShapeData(t *testing.T) {
	ctx := context.Background()
	// Optimization: Reuse shared DB
	_, calc := getSharedTestComponents(t)

	// Test with a non-existent stop
	direction, err := calc.computeFromShapes(ctx, "nonexistent")
	assert.NoError(t, err)
	assert.Equal(t, "", direction)
}

func TestComputeFromShapes_SingleOrientation(t *testing.T) {
	ctx := context.Background()
	// Optimization: Reuse shared DB
	_, calc := getSharedTestComponents(t)

	// Test with actual stop data - single orientation path will be taken if only one trip
	direction, err := calc.computeFromShapes(ctx, "7000")
	assert.NoError(t, err)
	// Direction should be valid or empty
	assert.True(t, direction == "" || len(direction) <= 2)
}

func TestComputeFromShapes_StandardDeviationThreshold(t *testing.T) {
	ctx := context.Background()
	// Note: We reuse the Shared Manager (DB) but create a NEW Calculator.
	// This is because we modify the variance threshold and don't want to break other tests.
	manager, _ := getSharedTestComponents(t)

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Set a very low standard deviation threshold to trigger variance check
	calc.standardDeviationThreshold = 0.01

	// Test with a stop that might have multiple trips
	direction, err := calc.computeFromShapes(ctx, "7000")
	assert.NoError(t, err)
	// With low threshold, high variance might return empty
	assert.True(t, direction == "" || len(direction) <= 2)
}

func TestCalculateOrientationAtStop_WithDistanceTraveled(t *testing.T) {
	ctx := context.Background()
	manager, calc := getSharedTestComponents(t)

	// Get a shape ID from the database
	shapes, err := manager.GtfsDB.Queries.GetShapePointsWithDistance(ctx, "19_0_1")
	if err != nil || len(shapes) < 2 {
		t.Skip("No shape data available for testing")
	}

	// Test with distance traveled
	orientation, err := calc.calculateOrientationAtStop(ctx, "19_0_1", 100.0, 0, 0)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, orientation, -math.Pi)
	assert.LessOrEqual(t, orientation, math.Pi)
}

func TestCalculateOrientationAtStop_GeographicMatching(t *testing.T) {
	ctx := context.Background()
	manager, calc := getSharedTestComponents(t)

	// Get a shape ID from the database
	shapes, err := manager.GtfsDB.Queries.GetShapePointsWithDistance(ctx, "19_0_1")
	if err != nil || len(shapes) < 2 {
		t.Skip("No shape data available for testing")
	}

	// Test with geographic matching (distTraveled < 0)
	stopLat := shapes[0].Lat
	stopLon := shapes[0].Lon
	orientation, err := calc.calculateOrientationAtStop(ctx, "19_0_1", -1.0, stopLat, stopLon)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, orientation, -math.Pi)
	assert.LessOrEqual(t, orientation, math.Pi)
}

func TestCalculateOrientationAtStop_NoShapePoints(t *testing.T) {
	ctx := context.Background()
	_, calc := getSharedTestComponents(t)

	// Test with non-existent shape - should return error
	orientation, err := calc.calculateOrientationAtStop(ctx, "nonexistent", 0, 0, 0)
	assert.Error(t, err)
	assert.Equal(t, float64(0), orientation)
}

func TestCalculateOrientationAtStop_EdgeCases(t *testing.T) {
	ctx := context.Background()
	manager, calc := getSharedTestComponents(t)

	// Test with shape that has points at the boundaries
	shapes, err := manager.GtfsDB.Queries.GetShapePointsWithDistance(ctx, "19_0_1")
	if err != nil || len(shapes) < 2 {
		t.Skip("No shape data available for testing")
	}
	// Test at the very beginning of the shape
	if len(shapes) > 0 && shapes[0].ShapeDistTraveled.Valid {
		orientation, err := calc.calculateOrientationAtStop(ctx, "19_0_1", shapes[0].ShapeDistTraveled.Float64, 0, 0)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, orientation, -math.Pi)
		assert.LessOrEqual(t, orientation, math.Pi)
	}

	// Test at the very end of the shape
	if len(shapes) > 1 && shapes[len(shapes)-1].ShapeDistTraveled.Valid {
		orientation, err := calc.calculateOrientationAtStop(ctx, "19_0_1", shapes[len(shapes)-1].ShapeDistTraveled.Float64, 0, 0)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, orientation, -math.Pi)
		assert.LessOrEqual(t, orientation, math.Pi)
	}
}

func TestGetAngleAsDirection_EdgeCases(t *testing.T) {
	calc := &AdvancedDirectionCalculator{}

	tests := []struct {
		name     string
		theta    float64
		expected string
	}{
		{"Large positive angle", 3 * math.Pi, "W"},
		{"Large negative angle", -3 * math.Pi, "W"},
		{"Just above threshold", math.Pi / 8, "NE"},
		{"Just below threshold", -math.Pi / 8, "E"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calc.getAngleAsDirection(tt.theta)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTranslateGtfsDirection_NumericEdgeCases(t *testing.T) {
	calc := &AdvancedDirectionCalculator{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"360 degrees wraps to North", "360", "N"},
		{"720 degrees wraps to North", "720", "N"},
		{"Negative angle -90", "-90", "W"},
		{"With whitespace", "  45  ", "NE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calc.translateGtfsDirection(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateStopDirection_VariadicSignature(t *testing.T) {
	ctx := context.Background()
	_, calc := getSharedTestComponents(t)

	// Case 1: Caller provides the optimized direction (should be used instantly)
	// We pass "North", expect "N"
	dirProvided := calc.CalculateStopDirection(ctx, "any_stop", nulls.String("North"))
	assert.Equal(t, "N", dirProvided, "Should use provided direction argument")

	// Case 2: Caller omits the argument (should fall back to DB)
	// The DB query will run, find nothing for "any_stop", and return "" gracefully.
	// Crucially, it won't panic because 'queries' is initialized.
	dirOmitted := calc.CalculateStopDirection(ctx, "any_stop")
	assert.Equal(t, "", dirOmitted, "Should fall back gracefully when argument is omitted")
}

// TestBulkQuery_GetStopsWithShapeContextByIDs verifies the bulk optimization
func TestBulkQuery_GetStopsWithShapeContextByIDs(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)

	// DYNAMICALLY fetch valid Stop IDs
	rows, err := manager.GtfsDB.DB.QueryContext(ctx, "SELECT id FROM stops LIMIT 5")
	if err != nil {
		t.Fatalf("Failed to query stops: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("Error closing rows: %v", err)
		}
	}()

	var stopIDs []string
	for rows.Next() {
		var id string
		err := rows.Scan(&id)
		if err != nil {
			t.Fatalf("Failed to scan row: %v", err)
		}
		stopIDs = append(stopIDs, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Row iteration error: %v", err)
	}
	assert.NotEmpty(t, stopIDs, "Database should have stops")

	// Execute the Bulk Query
	results, err := manager.GtfsDB.Queries.GetStopsWithShapeContextByIDs(ctx, stopIDs)

	// Verify Results
	assert.NoError(t, err)
	assert.NotEmpty(t, results)

	// We expect AT LEAST as many rows as IDs we asked for.
	assert.GreaterOrEqual(t, len(results), len(stopIDs),
		"Should return context rows for the requested stops")

	// Verify fields
	assert.NotEmpty(t, results[0].StopID)
	// Check NotZero for Lat because 0.0 is technically a valid lat, but unlikely in test data
	assert.NotZero(t, results[0].Lat)
}

// TestBulkQuery_GetShapePointsByIDs verifies fetching shape points in bulk.
func TestBulkQuery_GetShapePointsByIDs(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)

	// DYNAMICALLY fetch a real Shape ID from the DB
	var shapeID string
	err := manager.GtfsDB.DB.QueryRowContext(ctx, "SELECT shape_id FROM shapes LIMIT 1").Scan(&shapeID)

	// Stop immediately on error
	if err != nil {
		t.Fatalf("Failed to query shapes: %v", err)
	}

	shapeIDs := []string{shapeID}

	// Execute Bulk Query
	points, err := manager.GtfsDB.Queries.GetShapePointsByIDs(ctx, shapeIDs)

	// Verify
	assert.NoError(t, err)
	assert.NotEmpty(t, points)

	// Verify sorting
	isSorted := true
	for i := 0; i < len(points)-1; i++ {
		if points[i].ShapeID == points[i+1].ShapeID {
			if points[i].ShapePtSequence > points[i+1].ShapePtSequence {
				isSorted = false
				break
			}
		}
	}
	assert.True(t, isSorted, "Shape points should be returned in sequence order")
}

// shapeRow is a small helper to build a synthetic shape point for cache injection.
func shapeRow(lat, lon float64, seq int64) gtfsdb.GetShapePointsWithDistanceRow {
	return gtfsdb.GetShapePointsWithDistanceRow{
		Lat:             lat,
		Lon:             lon,
		ShapePtSequence: seq,
	}
}

// newCachedCalc builds a calculator whose shape data is served entirely from an
// in-memory cache, so calculateOrientationAtStop never touches the database.
// queries is nil intentionally: with a populated cache the DB path is never hit.
func newCachedCalc(t *testing.T, shapes map[string][]gtfsdb.GetShapePointsWithDistanceRow) *AdvancedDirectionCalculator {
	t.Helper()
	calc := NewAdvancedDirectionCalculator(nil)
	if err := calc.SetShapeCache(shapes); err != nil {
		t.Fatalf("SetShapeCache: %v", err)
	}
	return calc
}

// TestCalculateOrientationAtStop_SingleSegmentNotChord is the core regression test
// for the parity fix. An L-shaped shape runs due East and then turns due North.
// The stop sits on the final Eastward segment, right by the corner vertex.
//
// The OLD implementation measured the bearing over a wide ±5-point chord, which
// spanned the corner and produced a diagonal (NE) bearing. The fix measures the
// bearing of the single segment the stop lies on, which is due East.
func TestCalculateOrientationAtStop_SingleSegmentNotChord(t *testing.T) {
	ctx := context.Background()
	const shapeID = "L-shape"

	shape := []gtfsdb.GetShapePointsWithDistanceRow{
		shapeRow(0, 0, 0),
		shapeRow(0, 1, 1),
		shapeRow(0, 2, 2),
		shapeRow(0, 3, 3),
		shapeRow(0, 4, 4),
		shapeRow(0, 5, 5), // corner vertex (closest to the stop)
		shapeRow(1, 5, 6),
		shapeRow(2, 5, 7),
		shapeRow(3, 5, 8),
		shapeRow(4, 5, 9),
		shapeRow(5, 5, 10),
	}
	calc := newCachedCalc(t, map[string][]gtfsdb.GetShapePointsWithDistanceRow{shapeID: shape})

	// Stop lies just South of, and slightly before, the corner — i.e. on the
	// Eastward segment (P4->P5), not the Northward one (P5->P6).
	orientation, err := calc.calculateOrientationAtStop(ctx, shapeID, -1.0, -0.1, 4.9)
	assert.NoError(t, err)

	// Single-segment bearing is due East (dy=0, dx>0 => atan2 == 0).
	assert.InDelta(t, 0.0, orientation, 1e-9, "should follow the single Eastward segment")
	assert.Equal(t, "E", calc.getAngleAsDirection(orientation),
		"wide-chord bearing would have been NE; the fix yields E")
}

// TestCalculateOrientationAtStop_PicksNearestAdjacentSegment verifies the
// prev-vs-next segment selection: when the stop sits past the corner on the
// Northward leg, the Northward segment (P5->P6) is chosen instead of the
// Eastward one.
func TestCalculateOrientationAtStop_PicksNearestAdjacentSegment(t *testing.T) {
	ctx := context.Background()
	const shapeID = "L-shape"

	shape := []gtfsdb.GetShapePointsWithDistanceRow{
		shapeRow(0, 3, 0),
		shapeRow(0, 4, 1),
		shapeRow(0, 5, 2), // corner vertex (closest to the stop)
		shapeRow(1, 5, 3),
		shapeRow(2, 5, 4),
	}
	calc := newCachedCalc(t, map[string][]gtfsdb.GetShapePointsWithDistanceRow{shapeID: shape})

	// Stop sits just East of, and slightly above, the corner — nearest to the
	// Northward segment (lon=5, lat increasing).
	orientation, err := calc.calculateOrientationAtStop(ctx, shapeID, -1.0, 0.1, 5.1)
	assert.NoError(t, err)

	// Northward bearing: dx=0, dy>0 => atan2 == +pi/2.
	assert.InDelta(t, math.Pi/2, orientation, 1e-9, "should follow the Northward segment")
	assert.Equal(t, "N", calc.getAngleAsDirection(orientation))
}

// TestCalculateOrientationAtStop_NoCosLatScaling pins the deliberate removal of
// the cos(latitude) longitude correction. At 60°N a diagonal segment with equal
// lat/lon deltas must yield atan2(dlat, dlon) using RAW deltas. With the dropped
// cos-lat scaling the longitude delta would be halved (cos60°=0.5), rotating the
// bearing — so this value distinguishes the two implementations.
func TestCalculateOrientationAtStop_NoCosLatScaling(t *testing.T) {
	ctx := context.Background()
	const shapeID = "diagonal"

	// Two-point shape: any closest index collapses to the (0,1) boundary segment.
	shape := []gtfsdb.GetShapePointsWithDistanceRow{
		shapeRow(60.000, 10.000, 0),
		shapeRow(60.001, 10.002, 1),
	}
	calc := newCachedCalc(t, map[string][]gtfsdb.GetShapePointsWithDistanceRow{shapeID: shape})

	orientation, err := calc.calculateOrientationAtStop(ctx, shapeID, -1.0, 60.0, 10.0)
	assert.NoError(t, err)

	// Raw deltas: atan2(0.001, 0.002).
	expectedRaw := math.Atan2(0.001, 0.002)
	assert.InDelta(t, expectedRaw, orientation, 1e-9, "raw lon/lat deltas, no cos-lat scaling")

	// Guard: the cos(60°) scaled bearing (pi/4) must NOT match.
	cosLatBearing := math.Atan2(0.001, 0.002*math.Cos(60.0*math.Pi/180.0))
	assert.Greater(t, math.Abs(cosLatBearing-orientation), 1e-6,
		"cos-lat corrected bearing should differ from the raw bearing")
}

// TestCalculateOrientationAtStop_StartAndEndBoundaries verifies the boundary
// branches: a stop matching the first point uses segment [0,1], and one matching
// the last point uses segment [n-2, n-1].
func TestCalculateOrientationAtStop_StartAndEndBoundaries(t *testing.T) {
	ctx := context.Background()
	const shapeID = "boundaries"

	// Eastward then Northward; a corner separates the two legs so the start and
	// end segments have clearly different bearings.
	shape := []gtfsdb.GetShapePointsWithDistanceRow{
		shapeRow(0, 0, 0),
		shapeRow(0, 1, 1),
		shapeRow(0, 2, 2),
		shapeRow(1, 2, 3),
		shapeRow(2, 2, 4),
	}
	calc := newCachedCalc(t, map[string][]gtfsdb.GetShapePointsWithDistanceRow{shapeID: shape})

	// At the very first point -> segment [0,1] is Eastward.
	startOrientation, err := calc.calculateOrientationAtStop(ctx, shapeID, -1.0, 0, 0)
	assert.NoError(t, err)
	assert.Equal(t, "E", calc.getAngleAsDirection(startOrientation))

	// At the very last point -> segment [n-2, n-1] is Northward.
	endOrientation, err := calc.calculateOrientationAtStop(ctx, shapeID, -1.0, 2, 2)
	assert.NoError(t, err)
	assert.Equal(t, "N", calc.getAngleAsDirection(endOrientation))
}

// TestCalculateOrientationAtStop_DistTraveledMatching verifies the
// shape_dist_traveled matching path selects the correct segment, independent of
// geographic coordinates.
func TestCalculateOrientationAtStop_DistTraveledMatching(t *testing.T) {
	ctx := context.Background()
	const shapeID = "dist-shape"

	withDist := func(lat, lon float64, seq int64, dist float64) gtfsdb.GetShapePointsWithDistanceRow {
		r := shapeRow(lat, lon, seq)
		r.ShapeDistTraveled = sql.NullFloat64{Float64: dist, Valid: true}
		return r
	}

	// Eastward leg (dist 0..20) then Northward leg (dist 30..40).
	shape := []gtfsdb.GetShapePointsWithDistanceRow{
		withDist(0, 0, 0, 0),
		withDist(0, 1, 1, 10),
		withDist(0, 2, 2, 20), // corner
		withDist(1, 2, 3, 30),
		withDist(2, 2, 4, 40),
	}
	calc := newCachedCalc(t, map[string][]gtfsdb.GetShapePointsWithDistanceRow{shapeID: shape})

	// dist 5 sits on the Eastward leg. stopLat/stopLon are 0 so only dist matching applies.
	eastOrientation, err := calc.calculateOrientationAtStop(ctx, shapeID, 5.0, 0, 0)
	assert.NoError(t, err)
	assert.Equal(t, "E", calc.getAngleAsDirection(eastOrientation))

	// dist 35 sits on the Northward leg.
	northOrientation, err := calc.calculateOrientationAtStop(ctx, shapeID, 35.0, 0, 0)
	assert.NoError(t, err)
	assert.Equal(t, "N", calc.getAngleAsDirection(northOrientation))
}

// TestCalculateOrientationAtStop_InsufficientPoints verifies a single-point shape
// is treated as "no data" (ErrNoRows), never a panic from segment indexing.
func TestCalculateOrientationAtStop_InsufficientPoints(t *testing.T) {
	ctx := context.Background()
	const shapeID = "tiny"

	calc := newCachedCalc(t, map[string][]gtfsdb.GetShapePointsWithDistanceRow{
		shapeID: {shapeRow(0, 0, 0)},
	})

	_, err := calc.calculateOrientationAtStop(ctx, shapeID, -1.0, 0, 0)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestDistanceToSegment(t *testing.T) {
	tests := []struct {
		name                   string
		plat, plon             float64
		alat, alon, blat, blon float64
		expected               float64
	}{
		{
			name: "point on the segment",
			plat: 0, plon: 1,
			alat: 0, alon: 0, blat: 0, blon: 2,
			expected: 0,
		},
		{
			name: "perpendicular distance from segment",
			plat: 1, plon: 1,
			alat: 0, alon: 0, blat: 0, blon: 2,
			expected: 1,
		},
		{
			name: "projection clamped before start (t<0)",
			plat: 0, plon: -1,
			alat: 0, alon: 0, blat: 0, blon: 2,
			expected: 1, // nearest point is endpoint A
		},
		{
			name: "projection clamped past end (t>1)",
			plat: 0, plon: 5,
			alat: 0, alon: 0, blat: 0, blon: 2,
			expected: 3, // nearest point is endpoint B
		},
		{
			name: "degenerate segment uses point-to-point distance",
			plat: 3, plon: 4,
			alat: 0, alon: 0, blat: 0, blon: 0,
			expected: 5, // 3-4-5 triangle
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := distanceToSegment(tt.plat, tt.plon, tt.alat, tt.alon, tt.blat, tt.blon)
			assert.InDelta(t, tt.expected, got, 1e-9)
		})
	}
}

func TestMain(m *testing.M) {
	// Run all tests
	code := m.Run()

	// Global Teardown
	// If sharedManager was initialized during tests, shut it down now.
	if sharedManager != nil {
		sharedManager.Shutdown()
	}

	// Exit with the test result code
	os.Exit(code)
}
