package restapi

import (
	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
)

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
