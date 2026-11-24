package restapi

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
)

func TestShapeRowsToPoints(t *testing.T) {
	tests := []struct {
		name     string
		rows     []gtfsdb.GetShapePointsByTripIDRow
		expected []gtfs.ShapePoint
	}{
		{
			name:     "nil slice returns empty non-nil slice",
			rows:     nil,
			expected: []gtfs.ShapePoint{},
		},
		{
			name:     "empty slice returns empty slice",
			rows:     []gtfsdb.GetShapePointsByTripIDRow{},
			expected: []gtfs.ShapePoint{},
		},
		{
			name: "single row maps lat and lon correctly",
			rows: []gtfsdb.GetShapePointsByTripIDRow{
				{
					Lat: 47.6062,
					Lon: -122.3321,
				},
			},
			expected: []gtfs.ShapePoint{
				{
					Latitude:  47.6062,
					Longitude: -122.3321,
				},
			},
		},
		{
			name: "keeps input order",
			rows: []gtfsdb.GetShapePointsByTripIDRow{
				{
					Lat: 47.6000,
					Lon: -122.3300,
				},
				{
					Lat: 47.6050,
					Lon: -122.3350,
				},
				{
					Lat: 47.6100,
					Lon: -122.3400,
				},
			},
			expected: []gtfs.ShapePoint{
				{Latitude: 47.6000, Longitude: -122.3300},
				{Latitude: 47.6050, Longitude: -122.3350},
				{Latitude: 47.6100, Longitude: -122.3400},
			},
		},
		{
			name: "handles negative and zero coordinates accurately",
			rows: []gtfsdb.GetShapePointsByTripIDRow{
				{
					Lat: 0.0,
					Lon: 0.0,
				},
				{
					Lat: -33.8688,
					Lon: 151.2093,
				},
			},
			expected: []gtfs.ShapePoint{
				{Latitude: 0.0, Longitude: 0.0},
				{Latitude: -33.8688, Longitude: 151.2093},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pts := shapeRowsToPoints(tt.rows)
			assert.Equal(t, tt.expected, pts)
		})
	}
}
