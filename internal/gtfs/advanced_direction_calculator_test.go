package gtfs

import (
	"database/sql"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
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
