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

func shapePointsRowsToPoints(rows []gtfsdb.GetShapePointsByTripIDsRow) []gtfs.ShapePoint {
	pts := make([]gtfs.ShapePoint, len(rows))
	for i, sp := range rows {
		pts[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
	}
	return pts
}

type DistanceHelperCache struct {
	stopTimesByTrip map[string][]gtfsdb.StopTime
	shapePointsByTrip map[string][]gtfsdb.GetShapePointsByTripIDsRow
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) getStopDistanceAlongShape(ctx context.Context, tripID, stopID string, cache *DistanceHelperCache) float64 {
	var stopTimes []gtfsdb.StopTime
	var err error

	if cache != nil && cache.stopTimesByTrip[tripID] != nil {
		stopTimes = cache.stopTimesByTrip[tripID]
	} else {
		stopTimes, err = api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	}

	if err == nil {
		for _, st := range stopTimes {
			if st.StopID == stopID && st.ShapeDistTraveled.Valid {
				return st.ShapeDistTraveled.Float64
			}
		}
	}

	var shapePoints []gtfs.ShapePoint

	if cache != nil && cache.shapePointsByTrip[tripID] != nil {
		shapeRows := cache.shapePointsByTrip[tripID]
		if len(shapeRows) < 2 {
			return 0
		}
		shapePoints = shapePointsRowsToPoints(shapeRows)
	} else {
		shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
		if err != nil || len(shapeRows) < 2 {
			return 0
		}
		shapePoints = shapeRowsToPoints(shapeRows)
	}

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		return 0
	}

	return getDistanceAlongShape(stop.Lat, stop.Lon, shapePoints)
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) getVehicleDistanceAlongShapeContextual(ctx context.Context, tripID string, vehicle *gtfs.Vehicle, cache *DistanceHelperCache) float64 {
	if vehicle == nil || vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
		return 0
	}

	var shapePoints []gtfs.ShapePoint

	if cache != nil && cache.shapePointsByTrip[tripID] != nil {
		shapeRows := cache.shapePointsByTrip[tripID]
		if len(shapeRows) < 2 {
			return 0
		}
		shapePoints = shapePointsRowsToPoints(shapeRows)
	} else {
		shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
		if err != nil || len(shapeRows) < 2 {
			return 0
		}
		shapePoints = shapeRowsToPoints(shapeRows)
	}

	lat := float64(*vehicle.Position.Latitude)
	lon := float64(*vehicle.Position.Longitude)

	if vehicle.CurrentStopSequence != nil {
		var stopTimes []gtfsdb.StopTime
		var err error

		if cache != nil && cache.stopTimesByTrip[tripID] != nil {
			stopTimes = cache.stopTimesByTrip[tripID]
		} else {
			stopTimes, err = api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		}

		if err == nil && len(stopTimes) > 0 {
			currentSeq := int64(*vehicle.CurrentStopSequence)
			var prevStopDist, nextStopDist float64
			foundNext := false

			for i, st := range stopTimes {
				if st.StopSequence >= currentSeq {
					if st.ShapeDistTraveled.Valid {
						nextStopDist = st.ShapeDistTraveled.Float64
					} else {
						nextStopDist = api.getStopDistanceAlongShape(ctx, tripID, st.StopID, cache)
					}
					if i > 0 {
						if stopTimes[i-1].ShapeDistTraveled.Valid {
							prevStopDist = stopTimes[i-1].ShapeDistTraveled.Float64
						} else {
							prevStopDist = api.getStopDistanceAlongShape(ctx, tripID, stopTimes[i-1].StopID, cache)
						}
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
