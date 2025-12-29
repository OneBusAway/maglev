package restapi

import (
	"context"

	"github.com/OneBusAway/go-gtfs"
)

func (api *RestAPI) getStopDistanceAlongShape(ctx context.Context, tripID, stopID string) float64 {
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if err == nil {
		for _, st := range stopTimes {
			if st.StopID == stopID && st.ShapeDistTraveled.Valid {
				return st.ShapeDistTraveled.Float64
			}
		}
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if err != nil || len(shapeRows) < 2 {
		return 0
	}

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		return 0
	}

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
	}

	return getDistanceAlongShape(stop.Lat, stop.Lon, shapePoints)
}

func (api *RestAPI) getVehicleDistanceAlongShapeContextual(ctx context.Context, tripID string, vehicle *gtfs.Vehicle) float64 {
	if vehicle == nil || vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
		return 0
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if err != nil || len(shapeRows) < 2 {
		return 0
	}

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
	}

	lat := float64(*vehicle.Position.Latitude)
	lon := float64(*vehicle.Position.Longitude)

	if vehicle.CurrentStopSequence != nil {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		if err == nil && len(stopTimes) > 0 {
			currentSeq := int64(*vehicle.CurrentStopSequence)
			var prevStopDist, nextStopDist float64
			foundNext := false

			for i, st := range stopTimes {
				if st.StopSequence >= currentSeq {
					if st.ShapeDistTraveled.Valid {
						nextStopDist = st.ShapeDistTraveled.Float64
					} else {
						nextStopDist = api.getStopDistanceAlongShape(ctx, tripID, st.StopID)
					}
					if i > 0 {
						if stopTimes[i-1].ShapeDistTraveled.Valid {
							prevStopDist = stopTimes[i-1].ShapeDistTraveled.Float64
						} else {
							prevStopDist = api.getStopDistanceAlongShape(ctx, tripID, stopTimes[i-1].StopID)
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
