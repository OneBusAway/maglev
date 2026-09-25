package geo

import (
	"math"
	"os"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// squareRing returns a closed ring of a square with the given half-size in degrees.
func squareRing(centerLon, centerLat, half float64) [][2]float64 {
	return [][2]float64{
		{centerLon - half, centerLat - half}, {centerLon + half, centerLat - half},
		{centerLon + half, centerLat + half}, {centerLon - half, centerLat + half},
		{centerLon - half, centerLat - half},
	}
}

// signedArea reports the ring's winding: positive is counter-clockwise.
func signedArea(ring [][2]float64) float64 {
	area := 0.0
	for i := 0; i < len(ring)-1; i++ {
		area += ring[i][0]*ring[i+1][1] - ring[i+1][0]*ring[i][1]
	}
	return area
}

// maxDeviationMeters is the largest distance from any original vertex to the
// nearest segment of the simplified ring.
func maxDeviationMeters(original, simplified [][2]float64) float64 {
	scaleX, scaleY := metersPerDegree(original)
	worst := 0.0
	for _, p := range original {
		best := math.Inf(1)
		for i := 0; i < len(simplified)-1; i++ {
			best = math.Min(best, pointToSegmentMeters(p, simplified[i], simplified[i+1], scaleX, scaleY))
		}
		worst = math.Max(worst, best)
	}
	return worst
}

func alexandriaZone(t *testing.T) [][][][2]float64 {
	t.Helper()
	bytes, err := os.ReadFile("../../testdata/alexandria-flex.zip")
	require.NoError(t, err)
	static, err := gtfs.ParseStatic(bytes, gtfs.ParseStaticOptions{})
	require.NoError(t, err)
	require.Len(t, static.Locations, 1)
	return static.Locations[0].Geometry.Polygons
}

func TestSimplifyPolygons_AlexandriaRingFitsWithinBound(t *testing.T) {
	original := alexandriaZone(t)
	require.Len(t, original[0][0], 4239)

	result := SimplifyPolygons(original)

	ring := result.Polygons[0][0]
	assert.True(t, result.Changed)
	assert.Equal(t, 160.0, result.ToleranceMeters, "10 m doubles until every ring has <= 256 points")
	assert.LessOrEqual(t, len(ring), SimplifyMaxRingPoints)
	assert.Greater(t, len(ring), 100, "the simplification must not collapse the zone")
	assert.Equal(t, ring[0], ring[len(ring)-1], "the ring stays closed")
	assert.LessOrEqual(t, maxDeviationMeters(original[0][0], ring), result.ToleranceMeters)
	assert.Equal(t, math.Signbit(signedArea(original[0][0])), math.Signbit(signedArea(ring)), "winding is preserved")
	assert.Len(t, result.Polygons[0], 1, "the exterior ring is kept")
}

func TestSimplifyPolygons_Rings(t *testing.T) {
	collinearHole := [][2]float64{{0.001, 0.001}, {0.002, 0.001}, {0.003, 0.001}, {0.002, 0.0010001}, {0.001, 0.001}}
	triangleHole := [][2]float64{{0.001, 0.001}, {0.001, 0.004}, {0.004, 0.004}, {0.001, 0.001}}
	coincidentRing := make([][2]float64, SimplifyMaxRingPoints+44)
	for i := range coincidentRing {
		coincidentRing[i] = [2]float64{0.001, 0.001}
	}

	tests := []struct {
		name        string
		polygons    [][][][2]float64
		wantChanged bool
		wantRings   []int // ring point counts per polygon 0
	}{
		{
			name:        "square is unchanged and reports no change",
			polygons:    [][][][2]float64{{squareRing(0, 0, 0.01)}},
			wantChanged: false,
			wantRings:   []int{5},
		},
		{
			name:        "hole that collapses below four points is dropped",
			polygons:    [][][][2]float64{{squareRing(0, 0, 0.01), collinearHole}},
			wantChanged: true,
			wantRings:   []int{5},
		},
		{
			name:        "hole with four points is kept",
			polygons:    [][][][2]float64{{squareRing(0, 0, 0.01), triangleHole}},
			wantChanged: false,
			wantRings:   []int{5, 4},
		},
		{
			name:        "exterior ring is never dropped even when degenerate",
			polygons:    [][][][2]float64{{collinearHole}},
			wantChanged: true,
			wantRings:   []int{3},
		},
		{
			name:        "exterior ring of coincident vertices collapses to a closed pair",
			polygons:    [][][][2]float64{{coincidentRing}},
			wantChanged: true,
			wantRings:   []int{2},
		},
		{
			name:        "hole of coincident vertices is dropped",
			polygons:    [][][][2]float64{{squareRing(0, 0, 0.01), coincidentRing}},
			wantChanged: true,
			wantRings:   []int{5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SimplifyPolygons(tt.polygons)
			assert.Equal(t, tt.wantChanged, result.Changed)
			require.Len(t, result.Polygons, 1)
			require.Len(t, result.Polygons[0], len(tt.wantRings))
			for i, want := range tt.wantRings {
				assert.Len(t, result.Polygons[0][i], want, "ring %d", i)
			}
			assert.Equal(t, SimplifyInitialToleranceMeters, result.ToleranceMeters)
		})
	}
}

func TestSimplifyPolygons_MultiPolygonKeepsEveryPolygon(t *testing.T) {
	polygons := [][][][2]float64{{squareRing(0, 0, 0.01)}, {squareRing(1, 1, 0.01)}}
	result := SimplifyPolygons(polygons)
	assert.Len(t, result.Polygons, 2)
	assert.False(t, result.Changed)
}

func TestSimplifyPolygons_DoesNotMutateInput(t *testing.T) {
	// Vertex 1 is the farthest from vertex 0, so the first half of the split
	// ring is only two points long.
	ring := [][2]float64{{0, 0}, {0.01, 0.01}, {0.01, 0}, {0.005, 0.00001}, {0.002, 0}, {0, 0}}
	original := append([][2]float64{}, ring...)

	SimplifyPolygons([][][][2]float64{{ring}})

	assert.Equal(t, original, ring)
}
