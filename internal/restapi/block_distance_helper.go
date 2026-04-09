package restapi

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
)

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) getBlockDistanceToStop(ctx context.Context, targetTripID, targetStopID string, vehicle *gtfs.Vehicle, serviceDate time.Time, cache *DistanceHelperCache) float64 {
	if vehicle == nil || vehicle.Position == nil || vehicle.Trip == nil {
		return 0
	}

	var blockIDStr string
	if cache != nil && cache.blockIDByTrip != nil && cache.blockIDByTrip[targetTripID] != "" {
		blockIDStr = cache.blockIDByTrip[targetTripID]
	} else {
		blockID, err := api.GtfsManager.GtfsDB.Queries.GetBlockIDByTripID(ctx, targetTripID)
		if err != nil || !blockID.Valid || blockID.String == "" {
			// Fallback to single trip logic if no block
			if vehicle.Trip.ID.ID == targetTripID {
				targetDist := api.getStopDistanceAlongShape(ctx, targetTripID, targetStopID, cache)
				vehicleDist := api.getVehicleDistanceAlongShapeContextual(ctx, targetTripID, vehicle, cache)
				return targetDist - vehicleDist
			}
			return 0
		}
		blockIDStr = blockID.String
	}

	var blockTrips []gtfsdb.GetTripsByBlockIDRow
	if cache != nil && cache.tripsByBlock != nil && len(cache.tripsByBlock[blockIDStr]) > 0 {
		blockTrips = cache.tripsByBlock[blockIDStr]
	} else {
		var err error
		blockTrips, err = api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, sql.NullString{String: blockIDStr, Valid: true})
		if err != nil {
			return 0
		}
	}

	type TripInfo struct {
		TripID        string
		TotalDistance float64
		StartTime     int
	}

	activeTrips := []TripInfo{}
	for _, blockTrip := range blockTrips {
		isActive, err := api.GtfsManager.IsServiceActiveOnDate(ctx, blockTrip.ServiceID, serviceDate)
		if err != nil || isActive == 0 {
			continue
		}

		var stopTimes []gtfsdb.StopTime
		if cache != nil && cache.stopTimesByTrip[blockTrip.ID] != nil {
			stopTimes = cache.stopTimesByTrip[blockTrip.ID]
		} else {
			var fetchErr error
			stopTimes, fetchErr = api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
			if fetchErr != nil {
				continue
			}
		}
		if len(stopTimes) == 0 {
			continue
		}

		startTime := math.MaxInt
		for _, st := range stopTimes {
			if st.DepartureTime > 0 && int(st.DepartureTime) < startTime {
				startTime = int(st.DepartureTime)
			}
		}

		var shapeRows []gtfsdb.Shape
		var shapePoints []gtfs.ShapePoint
		if cache != nil && cache.shapePointsByTrip[blockTrip.ID] != nil {
			cachedShapeRows := cache.shapePointsByTrip[blockTrip.ID]
			if len(cachedShapeRows) > 1 {
				shapePoints = shapePointsRowsToPoints(cachedShapeRows)
			}
		} else {
			shapeRows, _ = api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, blockTrip.ID)
			if len(shapeRows) > 1 {
				shapePoints = shapeRowsToPoints(shapeRows)
			}
		}

		totalDist := 0.0
		if len(shapePoints) > 1 {
			totalDist = preCalculateCumulativeDistances(shapePoints)[len(shapePoints)-1]
		}

		activeTrips = append(activeTrips, TripInfo{
			TripID:        blockTrip.ID,
			TotalDistance: totalDist,
			StartTime:     startTime,
		})
	}

	sort.Slice(activeTrips, func(i, j int) bool {
		return activeTrips[i].StartTime < activeTrips[j].StartTime
	})

	cumulativeDist := 0.0
	vehicleBlockDist := -1.0
	targetBlockDist := -1.0

	for _, trip := range activeTrips {
		if trip.TripID == vehicle.Trip.ID.ID {
			vehicleDist := api.getVehicleDistanceAlongShapeContextual(ctx, trip.TripID, vehicle, cache)
			vehicleBlockDist = cumulativeDist + vehicleDist
		}
		if trip.TripID == targetTripID {
			targetDist := api.getStopDistanceAlongShape(ctx, trip.TripID, targetStopID, cache)
			targetBlockDist = cumulativeDist + targetDist
		}

		cumulativeDist += trip.TotalDistance
	}

	if vehicleBlockDist < 0 || targetBlockDist < 0 {
		return 0
	}

	return targetBlockDist - vehicleBlockDist
}
