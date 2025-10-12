package gtfs

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

func TestShapeCacheLoading(t *testing.T) {
	// Setup test database with shape data
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert test shapes
	testShapes := []struct {
		shapeID  string
		lat      float64
		lon      float64
		sequence int64
		dist     float64
	}{
		{"shape1", 40.0, -74.0, 0, 0.0},
		{"shape1", 40.001, -74.001, 1, 100.0},
		{"shape1", 40.002, -74.002, 2, 200.0},
		{"shape2", 41.0, -75.0, 0, 0.0},
		{"shape2", 41.001, -75.001, 1, 150.0},
	}

	for _, s := range testShapes {
		_, err = client.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
			ShapeID:           s.shapeID,
			Lat:               s.lat,
			Lon:               s.lon,
			ShapePtSequence:   s.sequence,
			ShapeDistTraveled: sql.NullFloat64{Float64: s.dist, Valid: true},
		})
		require.NoError(t, err)
	}

	// Create precomputer and load cache
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	cache, err := precomputer.loadShapeCache(ctx)

	require.NoError(t, err, "Cache loading should succeed")
	assert.NotNil(t, cache, "Cache should not be nil")
	assert.Len(t, cache, 2, "Should have 2 unique shape IDs")

	// Verify shape1 has 3 points
	shape1Points, exists := cache["shape1"]
	assert.True(t, exists, "shape1 should exist in cache")
	assert.Len(t, shape1Points, 3, "shape1 should have 3 points")

	// Verify shape2 has 2 points
	shape2Points, exists := cache["shape2"]
	assert.True(t, exists, "shape2 should exist in cache")
	assert.Len(t, shape2Points, 2, "shape2 should have 2 points")

	// Verify first point data integrity
	assert.Equal(t, 40.0, shape1Points[0].Lat, "First point latitude should match")
	assert.Equal(t, -74.0, shape1Points[0].Lon, "First point longitude should match")
	assert.Equal(t, int64(0), shape1Points[0].ShapePtSequence, "First point sequence should be 0")
}

func TestShapeCacheUsage(t *testing.T) {
	// Setup test database
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert test shapes
	shapes := []gtfsdb.CreateShapeParams{
		{ShapeID: "test_shape", Lat: 40.0, Lon: -74.0, ShapePtSequence: 0, ShapeDistTraveled: sql.NullFloat64{Float64: 0, Valid: true}},
		{ShapeID: "test_shape", Lat: 40.01, Lon: -74.01, ShapePtSequence: 1, ShapeDistTraveled: sql.NullFloat64{Float64: 100, Valid: true}},
		{ShapeID: "test_shape", Lat: 40.02, Lon: -74.02, ShapePtSequence: 2, ShapeDistTraveled: sql.NullFloat64{Float64: 200, Valid: true}},
	}

	for _, s := range shapes {
		_, err = client.Queries.CreateShape(ctx, s)
		require.NoError(t, err)
	}

	// Create calculator and set cache
	calc := NewAdvancedDirectionCalculator(client.Queries)

	// Build cache manually
	cache := map[string][]gtfsdb.GetShapePointsWithDistanceRow{
		"test_shape": {
			{Lat: 40.0, Lon: -74.0, ShapePtSequence: 0, ShapeDistTraveled: sql.NullFloat64{Float64: 0, Valid: true}},
			{Lat: 40.01, Lon: -74.01, ShapePtSequence: 1, ShapeDistTraveled: sql.NullFloat64{Float64: 100, Valid: true}},
			{Lat: 40.02, Lon: -74.02, ShapePtSequence: 2, ShapeDistTraveled: sql.NullFloat64{Float64: 200, Valid: true}},
		},
	}

	calc.SetShapeCache(cache)

	// Test that cached data is used
	orientation, err := calc.calculateOrientationAtStop(ctx, "test_shape", 50.0, 40.005, -74.005)
	assert.NoError(t, err, "Should calculate orientation using cache")
	assert.NotZero(t, orientation, "Should return non-zero orientation")
}

func TestShapeCacheFallbackToDatabase(t *testing.T) {
	// Setup test database
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert test shapes
	shapes := []gtfsdb.CreateShapeParams{
		{ShapeID: "db_shape", Lat: 40.0, Lon: -74.0, ShapePtSequence: 0, ShapeDistTraveled: sql.NullFloat64{Float64: 0, Valid: true}},
		{ShapeID: "db_shape", Lat: 40.01, Lon: -74.01, ShapePtSequence: 1, ShapeDistTraveled: sql.NullFloat64{Float64: 100, Valid: true}},
	}

	for _, s := range shapes {
		_, err = client.Queries.CreateShape(ctx, s)
		require.NoError(t, err)
	}

	// Create calculator WITHOUT setting cache
	calc := NewAdvancedDirectionCalculator(client.Queries)

	// Should fall back to database query
	orientation, err := calc.calculateOrientationAtStop(ctx, "db_shape", 0.0, 40.0, -74.0)
	assert.NoError(t, err, "Should fall back to database successfully")
	assert.NotZero(t, orientation, "Should return orientation from database")
}

func TestShapeCacheMiss(t *testing.T) {
	// Setup test database
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Create calculator with empty cache
	calc := NewAdvancedDirectionCalculator(client.Queries)
	emptyCache := make(map[string][]gtfsdb.GetShapePointsWithDistanceRow)
	calc.SetShapeCache(emptyCache)

	// Should return error for shape not in cache
	_, err = calc.calculateOrientationAtStop(ctx, "nonexistent_shape", 0.0, 40.0, -74.0)
	assert.Error(t, err, "Should return error for shape not in cache")
	assert.Equal(t, sql.ErrNoRows, err, "Should return ErrNoRows for missing shape")
}

func TestShapeCacheDataIntegrity(t *testing.T) {
	// Setup test database
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert shapes with specific coordinates
	testShapes := []gtfsdb.CreateShapeParams{
		{ShapeID: "verify_shape", Lat: 40.000, Lon: -74.000, ShapePtSequence: 0, ShapeDistTraveled: sql.NullFloat64{Float64: 0, Valid: true}},
		{ShapeID: "verify_shape", Lat: 40.005, Lon: -74.005, ShapePtSequence: 1, ShapeDistTraveled: sql.NullFloat64{Float64: 50, Valid: true}},
		{ShapeID: "verify_shape", Lat: 40.010, Lon: -74.010, ShapePtSequence: 2, ShapeDistTraveled: sql.NullFloat64{Float64: 100, Valid: true}},
	}

	for _, s := range testShapes {
		_, err = client.Queries.CreateShape(ctx, s)
		require.NoError(t, err)
	}

	// Load cache
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	cache, err := precomputer.loadShapeCache(ctx)
	require.NoError(t, err)

	// Verify cached data matches database
	cachedShape, exists := cache["verify_shape"]
	require.True(t, exists, "Shape should exist in cache")
	require.Len(t, cachedShape, 3, "Should have 3 points")

	// Query database directly
	dbShapes, err := client.Queries.GetShapePointsWithDistance(ctx, "verify_shape")
	require.NoError(t, err)
	require.Len(t, dbShapes, 3, "Database should have 3 points")

	// Compare cached data with database
	for i := range cachedShape {
		assert.Equal(t, dbShapes[i].Lat, cachedShape[i].Lat, "Point %d latitude should match", i)
		assert.Equal(t, dbShapes[i].Lon, cachedShape[i].Lon, "Point %d longitude should match", i)
		assert.Equal(t, dbShapes[i].ShapePtSequence, cachedShape[i].ShapePtSequence, "Point %d sequence should match", i)
		assert.Equal(t, dbShapes[i].ShapeDistTraveled, cachedShape[i].ShapeDistTraveled, "Point %d distance should match", i)
	}
}

func TestSetShapeCacheThreadSafety(t *testing.T) {
	// This test documents that SetShapeCache is NOT thread-safe
	// and must be called before concurrent operations

	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	calc := NewAdvancedDirectionCalculator(client.Queries)

	// Create a cache
	cache := map[string][]gtfsdb.GetShapePointsWithDistanceRow{
		"shape1": {
			{Lat: 40.0, Lon: -74.0, ShapePtSequence: 0},
		},
	}

	// Setting cache before any operations is safe
	calc.SetShapeCache(cache)

	// Verify cache is set
	assert.NotNil(t, calc.shapeCache, "Cache should be set")
	assert.Len(t, calc.shapeCache, 1, "Cache should have 1 shape")
}

func TestEmptyShapeCache(t *testing.T) {
	// Test behavior with no shapes in database
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Load cache from empty database
	precomputer := NewDirectionPrecomputer(client.Queries, client.DB)
	cache, err := precomputer.loadShapeCache(ctx)

	require.NoError(t, err, "Should handle empty database")
	assert.NotNil(t, cache, "Cache should not be nil")
	assert.Empty(t, cache, "Cache should be empty")
}
