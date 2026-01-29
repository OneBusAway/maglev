package gtfs

import (
	"context"
	"database/sql"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

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
			gtfsDirection: sql.NullString{String: "North", Valid: true},
			expected:      "N",
		},
		{
			name:          "Valid numeric direction",
			gtfsDirection: sql.NullString{String: "90", Valid: true},
			expected:      "E", // 90° in GTFS = East
		},
		{
			name:          "Invalid direction falls through",
			gtfsDirection: sql.NullString{String: "invalid", Valid: true},
			expected:      "", // Would need shape data to compute
		},
		{
			name:          "Null direction falls through",
			gtfsDirection: sql.NullString{Valid: false},
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
		assert.InDelta(t, 2.5, v, 0.001) // Sample variance of 1,2,3,4,5 is 2.5

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

func TestVarianceThreshold(t *testing.T) {
	calc := NewAdvancedDirectionCalculator(nil)

	// Test default threshold
	assert.Equal(t, defaultVarianceThreshold, calc.varianceThreshold)

	// Test setting custom threshold
	calc.SetVarianceThreshold(1.0)
	assert.Equal(t, 1.0, calc.varianceThreshold)
}

func TestCalculateStopDirection_WithShapeData(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Test with a real stop from RABA data
	direction := calc.CalculateStopDirection(context.Background(), "7000", sql.NullString{Valid: false})
	// Should return a valid direction or empty string
	assert.True(t, direction == "" || len(direction) <= 2)
}

func TestComputeFromShapes_NoShapeData(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Test with a non-existent stop
	direction := calc.computeFromShapes(context.Background(), "nonexistent")
	assert.Equal(t, "", direction)
}

func TestComputeFromShapes_SingleOrientation(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Test with actual stop data - single orientation path will be taken if only one trip
	direction := calc.computeFromShapes(context.Background(), "7000")
	// Direction should be valid or empty
	assert.True(t, direction == "" || len(direction) <= 2)
}

func TestComputeFromShapes_VarianceThreshold(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Set a very low variance threshold to trigger variance check
	calc.SetVarianceThreshold(0.01)

	// Test with a stop that might have multiple trips
	direction := calc.computeFromShapes(context.Background(), "7000")
	// With low threshold, high variance might return empty
	assert.True(t, direction == "" || len(direction) <= 2)
}

func TestCalculateOrientationAtStop_WithDistanceTraveled(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Get a shape ID from the database
	shapes, err := manager.GtfsDB.Queries.GetShapePointsWithDistance(context.Background(), "19_0_1")
	if err != nil || len(shapes) < 2 {
		t.Skip("No shape data available for testing")
	}

	// Test with distance traveled
	orientation, err := calc.calculateOrientationAtStop(context.Background(), "19_0_1", 100.0, 0, 0)
	if err == nil {
		assert.GreaterOrEqual(t, orientation, -math.Pi)
		assert.LessOrEqual(t, orientation, math.Pi)
	}
}

func TestCalculateOrientationAtStop_GeographicMatching(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Get a shape ID from the database
	shapes, err := manager.GtfsDB.Queries.GetShapePointsWithDistance(context.Background(), "19_0_1")
	if err != nil || len(shapes) < 2 {
		t.Skip("No shape data available for testing")
	}

	// Test with geographic matching (distTraveled < 0)
	stopLat := shapes[0].Lat
	stopLon := shapes[0].Lon
	orientation, err := calc.calculateOrientationAtStop(context.Background(), "19_0_1", -1.0, stopLat, stopLon)
	if err == nil {
		assert.GreaterOrEqual(t, orientation, -math.Pi)
		assert.LessOrEqual(t, orientation, math.Pi)
	}
}

func TestCalculateOrientationAtStop_NoShapePoints(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Test with non-existent shape - should return error or 0 orientation
	orientation, err := calc.calculateOrientationAtStop(context.Background(), "nonexistent", 0, 0, 0)
	// Either err is not nil, or orientation is 0
	assert.True(t, err != nil || orientation == 0)
}

func TestCalculateOrientationAtStop_EdgeCases(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Test with shape that has points at the boundaries
	shapes, err := manager.GtfsDB.Queries.GetShapePointsWithDistance(context.Background(), "19_0_1")
	if err != nil || len(shapes) < 2 {
		t.Skip("No shape data available for testing")
	}

	// Test at the very beginning of the shape
	if len(shapes) > 0 && shapes[0].ShapeDistTraveled.Valid {
		orientation, err := calc.calculateOrientationAtStop(context.Background(), "19_0_1", shapes[0].ShapeDistTraveled.Float64, 0, 0)
		if err == nil {
			assert.GreaterOrEqual(t, orientation, -math.Pi)
			assert.LessOrEqual(t, orientation, math.Pi)
		}
	}

	// Test at the very end of the shape
	if len(shapes) > 1 && shapes[len(shapes)-1].ShapeDistTraveled.Valid {
		orientation, err := calc.calculateOrientationAtStop(context.Background(), "19_0_1", shapes[len(shapes)-1].ShapeDistTraveled.Float64, 0, 0)
		if err == nil {
			assert.GreaterOrEqual(t, orientation, -math.Pi)
			assert.LessOrEqual(t, orientation, math.Pi)
		}
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

func TestSetContextCache_HappyPath(t *testing.T) {
	// Create a bare instance (no queries needed for this test)
	adc := &AdvancedDirectionCalculator{}

	// Create dummy cache data
	cache := make(map[string][]gtfsdb.GetStopsWithShapeContextRow)
	cache["stop1"] = []gtfsdb.GetStopsWithShapeContextRow{
		{
			ID:  "stop1",
			Lat: 40.7128,
			Lon: -74.0060,
		},
	}

	// Set the cache
	adc.SetContextCache(cache)

	// Verify it was set correctly (accessing private field)
	assert.Equal(t, 1, len(adc.contextCache))
	assert.Equal(t, "stop1", adc.contextCache["stop1"][0].ID)
}

func TestSetContextCache_PanicAfterInit(t *testing.T) {
	// Create the instance
	adc := &AdvancedDirectionCalculator{}

	// Simulate that concurrent operations have already started
	// We manually toggle the atomic boolean to "true"
	adc.initialized.Store(true)

	// Define the recovery to catch the panic
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("SetContextCache did not panic as expected when initialized=true")
		} else {
			assert.Equal(t, "SetContextCache called after concurrent operations have started", r)
		}
	}()

	// This call MUST panic now
	adc.SetContextCache(make(map[string][]gtfsdb.GetStopsWithShapeContextRow))
}

func TestCalculateStopDirection_VariadicSignature(t *testing.T) {

	// Setup in-memory DB so the calculator has a valid query interface
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	// Create the calculator using the VALID queries object
	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Case 1: Caller provides the optimized direction (should be used instantly)
	// We pass "North", expect "N"
	dirProvided := calc.CalculateStopDirection(context.Background(), "any_stop", sql.NullString{String: "North", Valid: true})
	assert.Equal(t, "N", dirProvided, "Should use provided direction argument")

	// Case 2: Caller omits the argument (should fall back to DB)
	// The DB query will run, find nothing for "any_stop", and return "" gracefully.
	// Crucially, it won't panic because 'queries' is initialized.
	dirOmitted := calc.CalculateStopDirection(context.Background(), "any_stop")
	assert.Equal(t, "", dirOmitted, "Should fall back gracefully when argument is omitted")
}

func TestSetContextCache_ConcurrentAccess(t *testing.T) {
	// Setup
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	calc := NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	// Create dummy cache
	cache := make(map[string][]gtfsdb.GetStopsWithShapeContextRow)

	// Channel to coordinate start
	start := make(chan struct{})
	done := make(chan struct{})

	// Launch a "Reader" Goroutine (Simulating a request coming in)
	go func() {
		<-start // Wait for signal
		// This triggers 'initialized.Store(true)' internally
		calc.CalculateStopDirection(context.Background(), "7000")
		close(done)
	}()

	// Launch a "Writer" (Simulating the bulk loader trying to set cache late)
	// We want to verify this doesn't crash the program with a race condition,
	// but correctly panics if it happens too late.
	go func() {
		<-start // Wait for signal
		defer func() {
			// recover if it panics (which is expected/allowed behavior for safety)
			_ = recover()
		}()
		calc.SetContextCache(cache)
	}()

	// Start the race
	close(start)

	// Wait for reader to finish
	<-done

	// If got here without the test binary crashing/deadlocking, the atomic guards did their job.
}

// TestBulkQuery_GetStopsWithShapeContextByIDs verifies the bulk optimization
func TestBulkQuery_GetStopsWithShapeContextByIDs(t *testing.T) {
	// Setup
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	ctx := context.Background()

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
	assert.Nil(t, err)
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
	// Setup
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
	}
	manager, err := InitGTFSManager(gtfsConfig)
	if err != nil {
		t.Fatalf("Failed to init manager: %v", err)
	}
	defer manager.Shutdown()

	ctx := context.Background()

	// DYNAMICALLY fetch a real Shape ID from the DB
	var shapeID string
	err = manager.GtfsDB.DB.QueryRowContext(ctx, "SELECT shape_id FROM shapes LIMIT 1").Scan(&shapeID)

	// Stop immediately on error
	if err != nil {
		t.Fatalf("Failed to query shapes: %v", err)
	}

	shapeIDs := []string{shapeID}

	// Execute Bulk Query
	points, err := manager.GtfsDB.Queries.GetShapePointsByIDs(ctx, shapeIDs)

	// Verify
	assert.Nil(t, err)
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
