package restapi

import (
	"context"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// positionVehicleForTrip follows the block assignment for anonymous updates,
// rather than letting a direct trip lookup select a second bus for that record.
func (api *RestAPI) positionVehicleForTrip(ctx context.Context, tripID string) *gtfs.Vehicle {
	for _, update := range api.GtfsManager.GetTripUpdatesForTrip(tripID) {
		if !namesVehicle(update) {
			if vehicle := api.preferredPositionVehicle(ctx, tripID); vehicle != nil {
				return vehicle
			}
		}
	}
	return api.GtfsManager.GetVehicleForTrip(ctx, tripID)
}

// preferredPositionVehicle mirrors Java's preferredVehicleByBlockId: the last
// live position entity for a block assigns its anonymous updates to one vehicle.
// A retained vehicle is not in the feed and cannot take that assignment.
// Cache the assignment for the request so status, lookup and predictions agree.
func (api *RestAPI) preferredPositionVehicle(ctx context.Context, tripID string) *gtfs.Vehicle {
	key := api.positionVehicleBlockKey(ctx, tripID)
	if key == "" {
		return nil
	}
	cache := snapshotCacheFrom(ctx)
	if cache == nil {
		return api.positionVehiclesByBlock(ctx)[key]
	}
	if cache.positionVehiclesByBlock == nil {
		cache.positionVehiclesByBlock = api.positionVehiclesByBlock(ctx)
	}
	return cache.positionVehiclesByBlock[key]
}

func (api *RestAPI) positionVehiclesByBlock(ctx context.Context) map[string]*gtfs.Vehicle {
	vehicles := make(map[string]*gtfs.Vehicle)
	positions := api.GtfsManager.GetLiveRealTimeVehicles()
	tripIDs := make([]string, 0, len(positions))
	for _, vehicle := range positions {
		if vehicle.Trip != nil {
			tripIDs = append(tripIDs, vehicle.Trip.ID.ID)
		}
	}
	trips, err := utils.QueryInBatches(ctx, tripIDs, api.GtfsManager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		warnIfRealDBError(err, "positionVehiclesByBlock: GetTripsByIDs failed")
		return vehicles
	}
	blockKeys := make(map[string]string, len(trips))
	for _, trip := range trips {
		blockKeys[trip.ID] = positionBlockKey(trip)
	}
	seen := make(map[string]bool)
	for _, vehicle := range positions {
		if vehicle.ID == nil || vehicle.ID.ID == "" || vehicle.Trip == nil || isTripUpdateVehicle(&vehicle) {
			continue
		}
		// Java accepts the first position for each vehicle before assigning
		// blocks, even if the feed repeats that vehicle on another trip.
		if seen[vehicle.ID.ID] {
			continue
		}
		seen[vehicle.ID.ID] = true
		if key := blockKeys[vehicle.Trip.ID.ID]; key != "" {
			vehicles[key] = &vehicle
		}
	}
	return vehicles
}

func (api *RestAPI) positionVehicleBlockKey(ctx context.Context, tripID string) string {
	trip, err := api.GtfsManager.GtfsDB.Queries.Memo(ctx).GetTrip(ctx, tripID)
	if err != nil {
		warnIfRealDBError(err, "positionVehicleBlockKey: GetTrip failed")
		return ""
	}
	return positionBlockKey(trip)
}

func positionBlockKey(trip gtfsdb.Trip) string {
	if blockID := nulls.StringOrEmpty(trip.BlockID); blockID != "" {
		return "block:" + blockID
	}
	return "trip:" + trip.ID
}
