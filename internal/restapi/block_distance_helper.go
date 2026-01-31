package restapi

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) getBlockDistanceToStop(ctx context.Context, targetTripID, targetStopID string, vehicle *gtfs.Vehicle, serviceDate time.Time) float64 {
	if vehicle == nil || vehicle.Position == nil || vehicle.Trip == nil {
		return 0
	}

	blockID, err := api.GtfsManager.GtfsDB.Queries.GetBlockIDByTripID(ctx, targetTripID)
	if err != nil || !blockID.Valid || blockID.String == "" {
		// Fallback to single trip logic if no block
		if vehicle.Trip.ID.ID == targetTripID {
			targetDist := api.getStopDistanceAlongShape(ctx, targetTripID, targetStopID)
			vehicleDist := api.getVehicleDistanceAlongShapeContextual(ctx, targetTripID, vehicle)
			return targetDist - vehicleDist
		}
		return 0
	}

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, blockID)
	if err != nil {
		return 0
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

		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}

		startTime := math.MaxInt
		for _, st := range stopTimes {
			if st.DepartureTime > 0 && int(st.DepartureTime) < startTime {
				startTime = int(st.DepartureTime)
			}
		}

		shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, blockTrip.ID)
		totalDist := 0.0
		if len(shapeRows) > 1 {
			shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
			for i, sp := range shapeRows {
				shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
			}
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
			vehicleDist := api.getVehicleDistanceAlongShapeContextual(ctx, trip.TripID, vehicle)
			vehicleBlockDist = cumulativeDist + vehicleDist
		}
		if trip.TripID == targetTripID {
			targetDist := api.getStopDistanceAlongShape(ctx, trip.TripID, targetStopID)
			targetBlockDist = cumulativeDist + targetDist
		}

		cumulativeDist += trip.TotalDistance
	}

	if vehicleBlockDist < 0 || targetBlockDist < 0 {
		return 0
	}

	return targetBlockDist - vehicleBlockDist
}
