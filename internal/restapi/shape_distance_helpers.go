package restapi

import (
	"context"

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

// getVehicleDistanceAlongShapeContextual projects the vehicle's GPS position
// onto the trip's shape polyline in metres, using the vehicle's CurrentStopSequence
// to constrain the search range on loop routes (prevents matching against a
// coordinate-identical earlier occurrence of the same segment).
//
// The prev/next stop bounds are computed via projectStopsInSequence — the same
// sequence-aware monotonic-cursor projection used everywhere else — so on a
// loop route the bounds land on the CORRECT occurrence of each stop rather
// than the geometrically-closest one, which would collapse to the earlier
// loop pass and mis-locate the vehicle by many kilometres.
//
// Publisher-provided shape_dist_traveled is ignored throughout for the same
// reason as getStopDistanceAlongShape — units are not standardised in GTFS
// and Java re-derives everything from geometry.
func (api *RestAPI) getVehicleDistanceAlongShapeContextual(ctx context.Context, tripID string, vehicle *gtfs.Vehicle) float64 {
	if vehicle == nil || vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
		return 0
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if err != nil || len(shapeRows) < 2 {
		return 0
	}

	shapePoints := shapeRowsToPoints(shapeRows)

	lat := float64(*vehicle.Position.Latitude)
	lon := float64(*vehicle.Position.Longitude)

	if vehicle.CurrentStopSequence != nil {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		if err == nil && len(stopTimes) > 0 {
			// Precompute every stop's distance in sequence order — the
			// monotonic cursor picks the right occurrence on loop routes.
			cumulativeDistances := preCalculateCumulativeDistances(shapePoints)
			stopByID := api.fetchStopCoordsForStopTimes(ctx, stopTimes)
			stopDistances := projectStopsInSequence(stopTimes, stopByID, shapePoints, cumulativeDistances)

			currentSeq := int64(*vehicle.CurrentStopSequence)
			var prevStopDist, nextStopDist float64
			foundNext := false
			for i, st := range stopTimes {
				if st.StopSequence >= currentSeq {
					nextStopDist = stopDistances[i]
					if i > 0 {
						prevStopDist = stopDistances[i-1]
					}
					foundNext = true
					break
				}
			}

			if foundNext {
				return getDistanceAlongShapeInRange(lat, lon, shapePoints, prevStopDist, nextStopDist)
			}
		}
	}

	return getDistanceAlongShape(lat, lon, shapePoints)
}
