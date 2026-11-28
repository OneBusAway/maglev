package restapi

import (
	"math"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
)

// wrapLongitude normalizes a longitude or longitude offset to [-180, 180].
func wrapLongitude(degrees float64) float64 {
	if degrees < -180 || degrees > 180 {
		return math.Remainder(degrees, 360)
	}
	return degrees
}

// shapeRowsToPoints converts a trip's shape rows to a gtfs.ShapePoint slice.
// The rows carry no shape_dist_traveled; cumulative distances are recomputed
// from scratch via preCalculateCumulativeDistances to ensure consistency.
func shapeRowsToPoints(rows []gtfsdb.GetShapePointsByTripIDRow) []gtfs.ShapePoint {
	pts := make([]gtfs.ShapePoint, len(rows))
	for i, sp := range rows {
		pts[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
	}
	return pts
}
