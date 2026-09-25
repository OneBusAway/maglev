package geo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// unitSquare is a 0.01° square at the origin with an optional centred hole.
func unitSquare(withHole bool) [][][][2]float64 {
	exterior := [][2]float64{{0, 0}, {0.01, 0}, {0.01, 0.01}, {0, 0.01}, {0, 0}}
	polygon := [][][2]float64{exterior}
	if withHole {
		polygon = append(polygon, [][2]float64{{0.004, 0.004}, {0.004, 0.006}, {0.006, 0.006}, {0.006, 0.004}, {0.004, 0.004}})
	}
	return [][][][2]float64{polygon}
}

func TestPointInPolygon(t *testing.T) {
	multi := append(unitSquare(false), [][][2]float64{{{1, 1}, {1.01, 1}, {1.01, 1.01}, {1, 1.01}, {1, 1}}})

	tests := []struct {
		name     string
		lat, lon float64
		polygons [][][][2]float64
		want     bool
	}{
		{"centre of square", 0.005, 0.005, unitSquare(false), true},
		{"outside square", 0.02, 0.005, unitSquare(false), false},
		{"inside hole is outside", 0.005, 0.005, unitSquare(true), false},
		{"between hole and exterior", 0.002, 0.002, unitSquare(true), true},
		{"on the exterior boundary", 0, 0.005, unitSquare(false), true},
		{"on a vertex", 0.01, 0.01, unitSquare(false), true},
		{"on a hole boundary is outside", 0.005, 0.004, unitSquare(true), false},
		{"second polygon of a multipolygon", 1.005, 1.005, multi, true},
		{"gap between multipolygon members", 0.5, 0.5, multi, false},
		{"empty geometry", 0, 0, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PointInPolygon(tt.lat, tt.lon, tt.polygons))
		})
	}
}

func TestNearestPointOnBoundary(t *testing.T) {
	// 0.01° of latitude is 6371010 m * 0.01 * pi/180 = 1111.95 m.
	distance, lon, lat := NearestPointOnBoundary(0.02, 0.005, unitSquare(false))
	assert.InDelta(t, 1111.95, distance, 1.0)
	assert.InDelta(t, 0.005, lon, 1e-9)
	assert.InDelta(t, 0.01, lat, 1e-9)

	// 0.0005° of latitude is 55.6 m; the hole's northern edge (lat 0.006) is nearest.
	distance, lon, lat = NearestPointOnBoundary(0.0055, 0.005, unitSquare(true))
	assert.InDelta(t, 55.6, distance, 0.5, "the hole boundary is the nearest ring segment")
	assert.InDelta(t, 0.006, lat, 1e-9)
	assert.InDelta(t, 0.005, lon, 1e-9)

	distance, _, _ = NearestPointOnBoundary(0.02, -0.01, unitSquare(false))
	assert.InDelta(t, 1572.5, distance, 2.0, "beyond a corner the nearest point is the vertex")
}

func TestPolygonIntersectsBounds(t *testing.T) {
	tests := []struct {
		name     string
		polygons [][][][2]float64
		bounds   CoordinateBounds
		want     bool
	}{
		{"polygon vertex inside bounds", unitSquare(false), CoordinateBounds{MinLat: -0.001, MaxLat: 0.001, MinLon: -0.001, MaxLon: 0.001}, true},
		{"bounds corner inside polygon", unitSquare(false), CoordinateBounds{MinLat: 0.002, MaxLat: 0.003, MinLon: 0.002, MaxLon: 0.003}, true},
		{"edge crossing only", unitSquare(false), CoordinateBounds{MinLat: -0.02, MaxLat: 0.02, MinLon: 0.005, MaxLon: 0.006}, true},
		{"disjoint", unitSquare(false), CoordinateBounds{MinLat: 0.02, MaxLat: 0.03, MinLon: 0.02, MaxLon: 0.03}, false},
		{"bounds entirely inside a hole", unitSquare(true), CoordinateBounds{MinLat: 0.0045, MaxLat: 0.0055, MinLon: 0.0045, MaxLon: 0.0055}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PolygonIntersectsBounds(tt.polygons, tt.bounds))
		})
	}
}
