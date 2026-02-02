package utils

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateBounds(t *testing.T) {
	lat := 38.627003
	lon := -121.530398
	radius := 500.0 // DEFAULT_SEARCH_RADIUS_WITHOUT_QUERY

	bounds := CalculateBounds(lat, lon, radius)

	latDiff := bounds.MaxLat - bounds.MinLat
	lonDiff := bounds.MaxLon - bounds.MinLon

	expectedLatDiff := 0.00898
	expectedLonDiff := 0.01153

	if latDiff < expectedLatDiff*0.99 || latDiff > expectedLatDiff*1.01 {
		t.Errorf("Lat diff %.10f is not close to expected %.10f", latDiff, expectedLatDiff)
	}

	if lonDiff < expectedLonDiff*0.99 || lonDiff > expectedLonDiff*1.01 {
		t.Errorf("Lon diff %.10f is not close to expected %.10f", lonDiff, expectedLonDiff)
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		name      string
		lat1      float64
		lon1      float64
		lat2      float64
		lon2      float64
		expected  float64
		tolerance float64
	}{
		{
			name:      "Same point (zero distance)",
			lat1:      40.7128,
			lon1:      -74.0060,
			lat2:      40.7128,
			lon2:      -74.0060,
			expected:  0,
			tolerance: 0.001,
		},
		{
			name:      "New York to Los Angeles",
			lat1:      40.7128,
			lon1:      -74.0060,
			lat2:      34.0522,
			lon2:      -118.2437,
			expected:  3935746, // approximately 3,936 km
			tolerance: 1000,    // 1km tolerance
		},
		{
			name:      "London to Paris",
			lat1:      51.5074,
			lon1:      -0.1278,
			lat2:      48.8566,
			lon2:      2.3522,
			expected:  343556, // approximately 344 km
			tolerance: 1000,
		},
		{
			name:      "Equator crossing (0,0 to 0,90)",
			lat1:      0,
			lon1:      0,
			lat2:      0,
			lon2:      90,
			expected:  10007543, // quarter of Earth's circumference
			tolerance: 10000,
		},
		{
			name:      "North-South along prime meridian",
			lat1:      0,
			lon1:      0,
			lat2:      45,
			lon2:      0,
			expected:  5003778, // 45 degrees north
			tolerance: 5000,
		},
		{
			name:      "Sydney to London (long distance)",
			lat1:      -33.8688,
			lon1:      151.2093,
			lat2:      51.5074,
			lon2:      -0.1278,
			expected:  16993933, // approximately 16,994 km
			tolerance: 10000,
		},
		{
			name:      "Small distance (1 meter approx)",
			lat1:      0.0,
			lon1:      0.0,
			lat2:      0.00001,
			lon2:      0.00001,
			expected:  1.57, // approximately 1.57 meters
			tolerance: 0.5,
		},
		{
			name:      "North Pole to Equator",
			lat1:      90,
			lon1:      0,
			lat2:      0,
			lon2:      0,
			expected:  10007543, // quarter of Earth's circumference
			tolerance: 10000,
		},
		{
			name:      "South Pole to Equator",
			lat1:      -90,
			lon1:      0,
			lat2:      0,
			lon2:      0,
			expected:  10007543, // quarter of Earth's circumference
			tolerance: 10000,
		},
		{
			name:      "Crossing International Date Line",
			lat1:      35.6762,
			lon1:      139.6503, // Tokyo
			lat2:      37.7749,
			lon2:      -122.4194, // San Francisco
			expected:  8280207,   // approximately 8,280 km
			tolerance: 10000,
		},
		{
			name:      "Negative to positive longitude",
			lat1:      40.0,
			lon1:      -10.0,
			lat2:      40.0,
			lon2:      10.0,
			expected:  1700008, // 20 degrees along 40th parallel
			tolerance: 5000,
		},
		{
			name:      "Very close points",
			lat1:      40.7128,
			lon1:      -74.0060,
			lat2:      40.7129,
			lon2:      -74.0061,
			expected:  13.5, // approximately 13.5 meters
			tolerance: 1.0,
		},
		{
			name:      "Antipodal points (opposite sides of Earth)",
			lat1:      40.0,
			lon1:      0.0,
			lat2:      -40.0,
			lon2:      180.0,
			expected:  20015087, // close to half Earth's circumference
			tolerance: 50000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Distance(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			assert.InDelta(t, tt.expected, result, tt.tolerance,
				"Distance should be approximately %f meters (±%f), got %f",
				tt.expected, tt.tolerance, result)
		})
	}
}

func TestDistance_Symmetry(t *testing.T) {
	// Distance from A to B should equal distance from B to A
	lat1, lon1 := 40.7128, -74.0060  // New York
	lat2, lon2 := 34.0522, -118.2437 // Los Angeles

	distAB := Distance(lat1, lon1, lat2, lon2)
	distBA := Distance(lat2, lon2, lat1, lon1)

	assert.InDelta(t, distAB, distBA, 0.0001, "Distance should be symmetric")
}

func TestDistance_TriangleInequality(t *testing.T) {
	// Distance A to C should be <= distance A to B + distance B to C
	latA, lonA := 40.7128, -74.0060  // New York
	latB, lonB := 41.8781, -87.6298  // Chicago
	latC, lonC := 34.0522, -118.2437 // Los Angeles

	distAB := Distance(latA, lonA, latB, lonB)
	distBC := Distance(latB, lonB, latC, lonC)
	distAC := Distance(latA, lonA, latC, lonC)

	assert.LessOrEqual(t, distAC, distAB+distBC,
		"Triangle inequality should hold: AC <= AB + BC")
}

func TestDistance_EdgeCases(t *testing.T) {
	t.Run("Both points at North Pole", func(t *testing.T) {
		// Any longitude at the pole should give zero distance
		dist := Distance(90, 0, 90, 180)
		assert.InDelta(t, 0, dist, 1, "Distance between two North Pole points should be zero")
	})

	t.Run("Both points at South Pole", func(t *testing.T) {
		dist := Distance(-90, 0, -90, 180)
		assert.InDelta(t, 0, dist, 1, "Distance between two South Pole points should be zero")
	})

	t.Run("Crossing equator", func(t *testing.T) {
		dist := Distance(-10, 0, 10, 0)
		expected := Distance(0, 0, 20, 0)
		assert.InDelta(t, expected, dist, 1000,
			"Distance crossing equator should match equivalent distance")
	})

	t.Run("180 degree longitude difference", func(t *testing.T) {
		dist := Distance(0, 0, 0, 180)
		// Half the Earth's circumference at equator
		expected := math.Pi * 6371000
		assert.InDelta(t, expected, dist, 10000,
			"180 degree longitude difference at equator should be half Earth's circumference")
	})

	t.Run("Negative latitudes and longitudes", func(t *testing.T) {
		// Southern hemisphere, western hemisphere
		dist := Distance(-33.9249, -18.4241, -34.6037, -58.3816)
		assert.Greater(t, dist, 0.0, "Distance should be positive")
		assert.Less(t, dist, 20037508.0, "Distance should be less than half Earth's circumference")
	})
}

func TestDistance_KnownDistances(t *testing.T) {
	tests := []struct {
		name     string
		city1    string
		lat1     float64
		lon1     float64
		city2    string
		lat2     float64
		lon2     float64
		expected float64
	}{
		{
			name:     "San Francisco to Seattle",
			city1:    "San Francisco",
			lat1:     37.7749,
			lon1:     -122.4194,
			city2:    "Seattle",
			lat2:     47.6062,
			lon2:     -122.3321,
			expected: 1093648, // ~1094 km
		},
		{
			name:     "Boston to Miami",
			city1:    "Boston",
			lat1:     42.3601,
			lon1:     -71.0589,
			city2:    "Miami",
			lat2:     25.7617,
			lon2:     -80.1918,
			expected: 2025337, // ~2025 km
		},
		{
			name:     "Tokyo to Seoul",
			city1:    "Tokyo",
			lat1:     35.6762,
			lon1:     139.6503,
			city2:    "Seoul",
			lat2:     37.5665,
			lon2:     126.9780,
			expected: 1149357, // ~1149 km
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Distance(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			// Allow 5km tolerance for known distances
			assert.InDelta(t, tt.expected, result, 5000,
				"%s to %s distance should be approximately %.0f meters",
				tt.city1, tt.city2, tt.expected)
		})
	}
}

func TestDistance_ConsistentResults(t *testing.T) {
	// Running the same calculation multiple times should give identical results
	lat1, lon1 := 40.7128, -74.0060
	lat2, lon2 := 34.0522, -118.2437

	result1 := Distance(lat1, lon1, lat2, lon2)
	result2 := Distance(lat1, lon1, lat2, lon2)
	result3 := Distance(lat1, lon1, lat2, lon2)

	assert.Equal(t, result1, result2, "Results should be identical")
	assert.Equal(t, result2, result3, "Results should be identical")
}

func TestDistance_OutputRange(t *testing.T) {
	// Distance should never return negative distance
	// and should never exceed half Earth's circumference (~20,037 km)
	tests := []struct {
		lat1, lon1, lat2, lon2 float64
	}{
		{0, 0, 0, 0},
		{90, 0, -90, 0},
		{45, 45, -45, -135},
		{-90, 180, 90, -180},
	}

	for _, tt := range tests {
		result := Distance(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
		assert.GreaterOrEqual(t, result, 0.0,
			"Distance should never be negative")
		assert.LessOrEqual(t, result, 20037508.0,
			"Distance should not exceed half Earth's circumference")
	}
}

func TestIsOutOfBounds(t *testing.T) {
	tests := []struct {
		name     string
		inner    CoordinateBounds
		outer    CoordinateBounds
		expected bool
	}{
		{
			name: "Inner fully inside outer",
			inner: CoordinateBounds{
				MinLat: 1, MaxLat: 2,
				MinLon: 1, MaxLon: 2,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: 0, MaxLon: 3,
			},
			expected: false,
		},
		{
			name: "Inner completely north of outer",
			inner: CoordinateBounds{
				MinLat: 5, MaxLat: 6,
				MinLon: 1, MaxLon: 2,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 4,
				MinLon: 0, MaxLon: 3,
			},
			expected: true,
		},
		{
			name: "Inner completely south of outer",
			inner: CoordinateBounds{
				MinLat: -6, MaxLat: -5,
				MinLon: 1, MaxLon: 2,
			},
			outer: CoordinateBounds{
				MinLat: -4, MaxLat: 4,
				MinLon: 0, MaxLon: 3,
			},
			expected: true,
		},
		{
			name: "Inner completely east of outer",
			inner: CoordinateBounds{
				MinLat: 1, MaxLat: 2,
				MinLon: 5, MaxLon: 6,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: 0, MaxLon: 4,
			},
			expected: true,
		},
		{
			name: "Inner completely west of outer",
			inner: CoordinateBounds{
				MinLat: 1, MaxLat: 2,
				MinLon: -6, MaxLon: -5,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: -4, MaxLon: 4,
			},
			expected: true,
		},
		{
			name: "Partial overlap (inner intersects outer)",
			inner: CoordinateBounds{
				MinLat: 2, MaxLat: 5,
				MinLon: 2, MaxLon: 5,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: 0, MaxLon: 3,
			},
			expected: false,
		},
		{
			name: "Equal bounds",
			inner: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: 0, MaxLon: 3,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: 0, MaxLon: 3,
			},
			expected: false,
		},
		{
			name: "Touching boundary exactly (edge case)",
			inner: CoordinateBounds{
				MinLat: 3, MaxLat: 4,
				MinLon: 1, MaxLon: 2,
			},
			outer: CoordinateBounds{
				MinLat: 0, MaxLat: 3,
				MinLon: 0, MaxLon: 3,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsOutOfBounds(tt.inner, tt.outer)
			assert.Equal(t, tt.expected, result)
		})
	}
}
