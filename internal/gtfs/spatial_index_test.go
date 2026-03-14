package gtfs

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/rtree"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// buildTestTree creates an R-tree with the provided stops for testing.
func buildTestTree(stops []gtfsdb.Stop) *rtree.RTree {
	tree := &rtree.RTree{}
	for _, stop := range stops {
		tree.Insert(
			[2]float64{stop.Lat, stop.Lon},
			[2]float64{stop.Lat, stop.Lon},
			stop,
		)
	}
	return tree
}

// testStops returns a fixed set of stops used across spatial index tests.
func testStops() []gtfsdb.Stop {
	return []gtfsdb.Stop{
		{ID: "stop1", Lat: 40.58, Lon: -122.39, Name: sql.NullString{String: "Downtown", Valid: true}},
		{ID: "stop2", Lat: 40.59, Lon: -122.38, Name: sql.NullString{String: "Midtown", Valid: true}},
		{ID: "stop3", Lat: 40.60, Lon: -122.37, Name: sql.NullString{String: "Uptown", Valid: true}},
		{ID: "stop4", Lat: 41.00, Lon: -122.00, Name: sql.NullString{String: "Faraway", Valid: true}},
	}
}

func TestQueryStopsInBounds(t *testing.T) {
	stops := testStops()
	tree := buildTestTree(stops)

	tests := []struct {
		name          string
		bounds        utils.CoordinateBounds
		expectedCount int
		expectedIDs   []string 
	}{
		{
			name: "NormalBoundingBox_SomeStops",
			bounds: utils.CoordinateBounds{
				MinLat: 40.57, MaxLat: 40.595,
				MinLon: -122.40, MaxLon: -122.37,
			},
			expectedCount: 2,
			expectedIDs:   []string{"stop1", "stop2"},
		},
		{
			name: "BoundingBox_NoStops",
			bounds: utils.CoordinateBounds{
				MinLat: 50.00, MaxLat: 51.00,
				MinLon: -80.00, MaxLon: -79.00,
			},
			expectedCount: 0,
		},
		{
			name: "BoundingBox_AllStops",
			bounds: utils.CoordinateBounds{
				MinLat: 40.00, MaxLat: 42.00,
				MinLon: -123.00, MaxLon: -121.00,
			},
			expectedCount: 4,
		},
		{
			name: "ZeroSizeBoundingBox_PointQuery",
			bounds: utils.CoordinateBounds{
				MinLat: 40.58, MaxLat: 40.58,
				MinLon: -122.39, MaxLon: -122.39,
			},
			expectedCount: 1,
			expectedIDs:   []string{"stop1"},
		},
		{
			name: "ExtremelyLargeBoundingBox",
			bounds: utils.CoordinateBounds{
				MinLat: -90.0, MaxLat: 90.0,
				MinLon: -180.0, MaxLon: 180.0,
			},
			expectedCount: 4,
		},
		{
			name: "SwappedMinMax_HandledByNormalization",
			bounds: utils.CoordinateBounds{
				MinLat: 40.595, MaxLat: 40.57,
				MinLon: -122.37, MaxLon: -122.40,
			},
			expectedCount: 2,
			expectedIDs:   []string{"stop1", "stop2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := queryStopsInBounds(tree, tt.bounds)
			assert.Len(t, results, tt.expectedCount)

			if len(tt.expectedIDs) > 0 {
				gotIDs := make([]string, len(results))
				for i, s := range results {
					gotIDs[i] = s.ID
				}
				for _, expectedID := range tt.expectedIDs {
					assert.Contains(t, gotIDs, expectedID)
				}
			}
		})
	}
}

func TestQueryStopsInBounds_NilTree(t *testing.T) {
	bounds := utils.CoordinateBounds{
		MinLat: 40.0, MaxLat: 41.0,
		MinLon: -123.0, MaxLon: -122.0,
	}

	results := queryStopsInBounds(nil, bounds)
	assert.Empty(t, results, "Nil tree should return empty slice")
	assert.NotNil(t, results, "Should return non-nil empty slice, not nil")
}

func TestQueryStopsInBounds_EmptyTree(t *testing.T) {
	tree := &rtree.RTree{}
	bounds := utils.CoordinateBounds{
		MinLat: -90.0, MaxLat: 90.0,
		MinLon: -180.0, MaxLon: 180.0,
	}

	results := queryStopsInBounds(tree, bounds)
	assert.Empty(t, results, "Empty tree should return no results")
}

func TestBuildStopSpatialIndex_WithRABA(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)
	require.NotNil(t, manager.stopSpatialIndex, "Spatial index should be built during initialization")

	// Query a bounding box around the RABA service area (Redding, CA)
	bounds := utils.CoordinateBounds{
		MinLat: 40.50, MaxLat: 40.70,
		MinLon: -122.50, MaxLon: -122.30,
	}

	results := queryStopsInBounds(manager.stopSpatialIndex, bounds)
	assert.NotEmpty(t, results, "Should find stops in the RABA service area")

	for _, stop := range results {
		assert.NotEmpty(t, stop.ID, "Stop ID should not be empty")
		assert.NotZero(t, stop.Lat, "Stop latitude should not be zero")
		assert.NotZero(t, stop.Lon, "Stop longitude should not be zero")
	}
}

func TestMinMaxHelpers(t *testing.T) {
	tests := []struct {
		name        string
		a, b        float64
		expectedMin float64
		expectedMax float64
	}{
		{"PositiveValues", 3.0, 5.0, 3.0, 5.0},
		{"NegativeValues", -5.0, -3.0, -5.0, -3.0},
		{"MixedSigns", -1.0, 1.0, -1.0, 1.0},
		{"EqualValues", 4.0, 4.0, 4.0, 4.0},
		{"ZeroAndPositive", 0.0, 7.0, 0.0, 7.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedMin, min(tt.a, tt.b))
			assert.Equal(t, tt.expectedMax, max(tt.a, tt.b))
		})
	}
}
