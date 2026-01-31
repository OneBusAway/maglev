package restapi

import (
	"context"
	"math"
	"sort"
	"time"
)

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) getBlockSequenceForStopSequence(ctx context.Context, tripID string, stopSequence int, serviceDate time.Time) int {
	blockID, err := api.GtfsManager.GtfsDB.Queries.GetBlockIDByTripID(ctx, tripID)
	if err != nil || !blockID.Valid || blockID.String == "" {
		// Fallback to simpler logic if no block
		return stopSequence
	}

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, blockID)
	if err != nil {
		return 0
	}

	type TripWithDetails struct {
		TripID    string
		StartTime int
	}

	activeTrips := []TripWithDetails{}

	for _, blockTrip := range blockTrips {
		isActive, err := api.GtfsManager.IsServiceActiveOnDate(ctx, blockTrip.ServiceID, serviceDate)
		if err != nil || isActive == 0 {
			continue
		}

		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}

		startTime := int64(math.MaxInt64)
		for _, st := range stopTimes {
			if st.DepartureTime > 0 && st.DepartureTime < startTime {
				startTime = st.DepartureTime
			}
		}

		if startTime != math.MaxInt64 {
			activeTrips = append(activeTrips, TripWithDetails{
				TripID:    blockTrip.ID,
				StartTime: int(startTime),
			})
		}
	}

	sort.Slice(activeTrips, func(i, j int) bool {
		return activeTrips[i].StartTime < activeTrips[j].StartTime
	})

	blockSequence := 0
	for _, trip := range activeTrips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.TripID)
		if err != nil {
			continue
		}

		if trip.TripID == tripID {
			return blockSequence + stopSequence
		}
		blockSequence += len(stopTimes)
	}

	return stopSequence
}
