package restapi

import (
	"database/sql"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
)

func TestShapeRowsToPoints(t *testing.T) {
	tests := []struct {
		name     string
		rows     []gtfsdb.Shape
		expected []gtfs.ShapePoint
	}{
		{
			name:     "nil slice returns empty non-nil slice",
			rows:     nil,
			expected: []gtfs.ShapePoint{},
		},
		{
			name:     "empty slice returns empty slice",
			rows:     []gtfsdb.Shape{},
			expected: []gtfs.ShapePoint{},
		},
		{
			name: "single row maps lat and lon correctly",
			rows: []gtfsdb.Shape{
				{
					ID:                1,
					ShapeID:           "shape_1",
					Lat:               47.6062,
					Lon:               -122.3321,
					ShapePtSequence:   1,
					ShapeDistTraveled: sql.NullFloat64{Valid: true, Float64: 10.5},
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
			name: "keeps input order without sorting by sequence and drops dist traveled",
			rows: []gtfsdb.Shape{
				{
					ID:                10,
					ShapeID:           "shape_1",
					Lat:               47.6000,
					Lon:               -122.3300,
					ShapePtSequence:   3,
					ShapeDistTraveled: sql.NullFloat64{Valid: true, Float64: 0.0},
				},
				{
					ID:                11,
					ShapeID:           "shape_1",
					Lat:               47.6050,
					Lon:               -122.3350,
					ShapePtSequence:   1,
					ShapeDistTraveled: sql.NullFloat64{Valid: true, Float64: 550.2},
				},
				{
					ID:                12,
					ShapeID:           "shape_1",
					Lat:               47.6100,
					Lon:               -122.3400,
					ShapePtSequence:   2,
					ShapeDistTraveled: sql.NullFloat64{Valid: false},
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
			rows: []gtfsdb.Shape{
				{
					ID:              20,
					ShapeID:         "shape_zero",
					Lat:             0.0,
					Lon:             0.0,
					ShapePtSequence: 1,
				},
				{
					ID:              21,
					ShapeID:         "shape_neg",
					Lat:             -33.8688,
					Lon:             151.2093,
					ShapePtSequence: 2,
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
