package restapi

import (
	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
)

// shapeRowsToPoints converts database shape rows to gtfs.ShapePoint slice.
// ShapeDistTraveled is intentionally dropped; cumulative distances are recomputed
// from scratch via preCalculateCumulativeDistances to ensure consistency.
func shapeRowsToPoints(rows []gtfsdb.Shape) []gtfs.ShapePoint {
	pts := make([]gtfs.ShapePoint, len(rows))
	for i, sp := range rows {
		pts[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
	}
	return pts
}
