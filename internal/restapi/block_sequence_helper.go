package restapi

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"
)

func (api *RestAPI) getBlockSequenceForStopSequence(ctx context.Context, tripID string, stopSequence int, serviceDate time.Time) int {
	blockID, err := api.GtfsManager.GtfsDB.Queries.GetBlockIDByTripID(ctx, tripID)
	if err != nil || !blockID.Valid || blockID.String == "" {
		return rawSequenceToOrdinal(api, ctx, tripID, stopSequence)
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

	slices.SortFunc(activeTrips, func(a, b TripWithDetails) int {
		return cmp.Compare(a.StartTime, b.StartTime)
	})

	blockSequence := 0
	for _, trip := range activeTrips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.TripID)
		if err != nil {
			continue
		}

		if trip.TripID == tripID {
			for i, st := range stopTimes {
				if int(st.StopSequence) == stopSequence {
					return blockSequence + i
				}
			}
			return blockSequence
		}
		blockSequence += len(stopTimes)
	}

	return rawSequenceToOrdinal(api, ctx, tripID, stopSequence)
}

// rawSequenceToOrdinal converts a raw GTFS stop_sequence to a 0-based ordinal
// by looking up the trip's stop times and finding the matching position.
func rawSequenceToOrdinal(api *RestAPI, ctx context.Context, tripID string, stopSequence int) int {
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if err != nil {
		return 0
	}
	for i, st := range stopTimes {
		if int(st.StopSequence) == stopSequence {
			return i
		}
	}
	return 0
}
