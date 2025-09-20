package utils

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearingBetweenPoints(t *testing.T) {
	tests := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		expected               float64
		tolerance              float64
	}{
		{
			name:      "North direction",
			lat1:      40.0,
			lon1:      -122.0,
			lat2:      41.0,
			lon2:      -122.0,
			expected:  0.0,
			tolerance: 1.0,
		},
		{
			name:      "East direction",
			lat1:      40.0,
			lon1:      -122.0,
			lat2:      40.0,
			lon2:      -121.0,
			expected:  90.0,
			tolerance: 1.0,
		},
		{
			name:      "Northeast direction",
			lat1:      40.0,
			lon1:      -122.0,
			lat2:      40.7,
			lon2:      -121.3,
			expected:  45.0,
			tolerance: 10.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bearing := BearingBetweenPoints(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			assert.InDelta(t, tt.expected, bearing, tt.tolerance)
		})
	}
}

func TestBearingToCompass(t *testing.T) {
	tests := []struct {
		bearing  float64
		expected string
	}{
		{0.0, "N"},
		{45.0, "NE"},
		{90.0, "E"},
		{135.0, "SE"},
		{180.0, "S"},
		{225.0, "SW"},
		{270.0, "W"},
		{315.0, "NW"},
		{360.0, "N"},
		{22.0, "N"},
		{23.0, "NE"},
		{67.0, "NE"},
		{68.0, "E"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%.1f degrees", tt.bearing), func(t *testing.T) {
			result := BearingToCompass(tt.bearing)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCompassDirection(t *testing.T) {
	tests := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		expected               string
	}{
		{
			name:     "North direction",
			lat1:     40.0,
			lon1:     -122.0,
			lat2:     41.0,
			lon2:     -122.0,
			expected: "N",
		},
		{
			name:     "East direction",
			lat1:     40.0,
			lon1:     -122.0,
			lat2:     40.0,
			lon2:     -121.0,
			expected: "E",
		},
		{
			name:     "South direction",
			lat1:     40.0,
			lon1:     -122.0,
			lat2:     39.0,
			lon2:     -122.0,
			expected: "S",
		},
		{
			name:     "West direction",
			lat1:     40.0,
			lon1:     -122.0,
			lat2:     40.0,
			lon2:     -123.0,
			expected: "W",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CompassDirection(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			assert.Equal(t, tt.expected, result)
		})
	}
}
