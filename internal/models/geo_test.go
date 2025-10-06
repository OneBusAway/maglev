package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComparePoints(t *testing.T) {
	tests := []struct {
		name     string
		a        CoordinatePoint
		b        CoordinatePoint
		expected int
	}{
		{
			name:     "a.Lat < b.Lat",
			a:        CoordinatePoint{Lat: 10.0, Lon: 20.0},
			b:        CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expected: -1,
		},
		{
			name:     "a.Lat > b.Lat",
			a:        CoordinatePoint{Lat: 20.0, Lon: 20.0},
			b:        CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expected: 1,
		},
		{
			name:     "Equal Lat, a.Lon < b.Lon",
			a:        CoordinatePoint{Lat: 15.0, Lon: 10.0},
			b:        CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expected: -1,
		},
		{
			name:     "Equal Lat, a.Lon > b.Lon",
			a:        CoordinatePoint{Lat: 15.0, Lon: 30.0},
			b:        CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expected: 1,
		},
		{
			name:     "Identical points",
			a:        CoordinatePoint{Lat: 15.0, Lon: 20.0},
			b:        CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ComparePoints(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewEdge(t *testing.T) {
	tests := []struct {
		name      string
		a         CoordinatePoint
		b         CoordinatePoint
		expectedA CoordinatePoint
		expectedB CoordinatePoint
	}{
		{
			name:      "a < b, should not swap",
			a:         CoordinatePoint{Lat: 10.0, Lon: 20.0},
			b:         CoordinatePoint{Lat: 15.0, Lon: 25.0},
			expectedA: CoordinatePoint{Lat: 10.0, Lon: 20.0},
			expectedB: CoordinatePoint{Lat: 15.0, Lon: 25.0},
		},
		{
			name:      "a > b, should swap",
			a:         CoordinatePoint{Lat: 20.0, Lon: 30.0},
			b:         CoordinatePoint{Lat: 15.0, Lon: 25.0},
			expectedA: CoordinatePoint{Lat: 15.0, Lon: 25.0},
			expectedB: CoordinatePoint{Lat: 20.0, Lon: 30.0},
		},
		{
			name:      "Equal Lat, a.Lon > b.Lon, should swap",
			a:         CoordinatePoint{Lat: 15.0, Lon: 30.0},
			b:         CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expectedA: CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expectedB: CoordinatePoint{Lat: 15.0, Lon: 30.0},
		},
		{
			name:      "Identical points",
			a:         CoordinatePoint{Lat: 15.0, Lon: 20.0},
			b:         CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expectedA: CoordinatePoint{Lat: 15.0, Lon: 20.0},
			expectedB: CoordinatePoint{Lat: 15.0, Lon: 20.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edge := NewEdge(tt.a, tt.b)
			assert.Equal(t, tt.expectedA, edge.A)
			assert.Equal(t, tt.expectedB, edge.B)
		})
	}
}

func TestEdgeJSON(t *testing.T) {
	edge := Edge{
		A: CoordinatePoint{Lat: 47.6062, Lon: -122.3321},
		B: CoordinatePoint{Lat: 47.6205, Lon: -122.3493},
	}

	jsonData, err := json.Marshal(edge)
	assert.NoError(t, err)

	var unmarshaledEdge Edge
	err = json.Unmarshal(jsonData, &unmarshaledEdge)
	assert.NoError(t, err)

	assert.Equal(t, edge.A.Lat, unmarshaledEdge.A.Lat)
	assert.Equal(t, edge.A.Lon, unmarshaledEdge.A.Lon)
	assert.Equal(t, edge.B.Lat, unmarshaledEdge.B.Lat)
	assert.Equal(t, edge.B.Lon, unmarshaledEdge.B.Lon)
}

func TestCoordinatePointJSON(t *testing.T) {
	point := CoordinatePoint{Lat: 38.542661, Lon: -121.743914}

	jsonData, err := json.Marshal(point)
	assert.NoError(t, err)

	var unmarshaledPoint CoordinatePoint
	err = json.Unmarshal(jsonData, &unmarshaledPoint)
	assert.NoError(t, err)

	assert.Equal(t, point.Lat, unmarshaledPoint.Lat)
	assert.Equal(t, point.Lon, unmarshaledPoint.Lon)
}

func TestEdgeWithZeroValues(t *testing.T) {
	edge := NewEdge(
		CoordinatePoint{Lat: 0, Lon: 0},
		CoordinatePoint{Lat: 0, Lon: 0},
	)

	assert.Equal(t, 0.0, edge.A.Lat)
	assert.Equal(t, 0.0, edge.A.Lon)
	assert.Equal(t, 0.0, edge.B.Lat)
	assert.Equal(t, 0.0, edge.B.Lon)
}
