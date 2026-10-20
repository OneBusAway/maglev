package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/nulls"
)

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

func TestRecentShapesEvictsLeastRecentlyUsedPoints(t *testing.T) {
	points := func(n int) []gtfsdb.GetShapePointsWithDistanceRow {
		return make([]gtfsdb.GetShapePointsWithDistanceRow, n)
	}
	cache := newRecentShapes(5)
	cache.add("A", points(2))
	cache.add("B", points(2))

	// Reading A makes B the least recently used, so adding C (2 points, 6 in
	// total) evicts B.
	_, ok := cache.get("A")
	assert.True(t, ok)
	cache.add("C", points(2))

	_, ok = cache.get("B")
	assert.False(t, ok, "B should have been evicted")
	_, ok = cache.get("A")
	assert.True(t, ok)
	_, ok = cache.get("C")
	assert.True(t, ok)
	assert.Equal(t, 4, cache.totalPoints)

	// A shape larger than the whole budget is kept on its own.
	cache.add("huge", points(10))
	assert.Equal(t, 1, cache.order.Len())
	_, ok = cache.get("huge")
	assert.True(t, ok)
}

// shapeReadCountingDB counts reads of a shape's points, and fails them while
// fail is set.
type shapeReadCountingDB struct {
	gtfsdb.DBTX
	reads atomic.Int64
	fail  atomic.Bool
}

func (db *shapeReadCountingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, "-- name: GetShapePointsWithDistance :many") {
		db.reads.Add(1)
		if db.fail.Load() {
			return nil, errors.New("temporary shape read failure")
		}
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

// TestPrecomputeReadsEachShapeOnce checks that the precomputer's workers share
// shapes: all four stops lie on one shape, which must be read once. The
// directions it stores must be the ones the uncached calculator computes.
func TestPrecomputeReadsEachShapeOnce(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "900", "2000", "3000"}, increasingDirectionShape))
	db := &shapeReadCountingDB{DBTX: client.DB}

	require.NoError(t, NewDirectionPrecomputer(gtfsdb.New(db), client.DB).PrecomputeAllDirections(context.Background()))

	assert.EqualValues(t, 1, db.reads.Load())
	for stopID, expected := range map[string]string{"S1": "E", "S2": "E", "S3": "NE", "S4": "N"} {
		stop, err := client.Queries.GetStop(context.Background(), stopID)
		require.NoError(t, err)
		assert.Equal(t, expected, nulls.StringOrEmpty(stop.Direction), stopID)
	}
}

// TestShapePointsReadErrorIsNotCached checks that a failed read is retried on
// the next lookup instead of being remembered.
func TestShapePointsReadErrorIsNotCached(t *testing.T) {
	client := directionTestClient(t, buildDirectionFeed(t, []string{"0", "900", "2000", "3000"}, increasingDirectionShape))
	db := &shapeReadCountingDB{DBTX: client.DB}
	calculator := NewAdvancedDirectionCalculator(gtfsdb.New(db))
	calculator.recentShapes = newRecentShapes(precomputeShapeCachePoints)

	db.fail.Store(true)
	_, err := calculator.shapePoints(context.Background(), "SH1")
	require.Error(t, err)

	db.fail.Store(false)
	points, err := calculator.shapePoints(context.Background(), "SH1")
	require.NoError(t, err)
	assert.Len(t, points, len(increasingDirectionShape))
	assert.EqualValues(t, 2, db.reads.Load())
}
