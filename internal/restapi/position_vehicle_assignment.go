package restapi

import (
	"context"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// positionVehicleForTrip follows the shift assignment for anonymous updates,
// rather than letting a direct trip lookup select a second bus for that record.
func (api *RestAPI) positionVehicleForTrip(ctx context.Context, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	for _, update := range api.GtfsManager.GetTripUpdatesForTrip(tripID) {
		if !namesVehicle(update) {
			if vehicle := api.preferredPositionVehicle(ctx, tripID, serviceDate); vehicle != nil {
				return vehicle
			}
		}
	}
	return api.GtfsManager.GetVehicleForTrip(ctx, tripID)
}

// preferredPositionVehicle assigns anonymous updates to one live vehicle in the
// same service-date shift used by record and snapshot placement. A reused static
// block_id must not connect overlapping shifts or different service dates.
func (api *RestAPI) preferredPositionVehicle(ctx context.Context, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	if _, err := api.gtfsQueries(ctx).Memo(ctx).GetTrip(ctx, tripID); err != nil {
		warnIfRealDBError(err, "preferredPositionVehicle: GetTrip failed")
		return nil
	}
	tripIDs := api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate)
	if !slices.Contains(tripIDs, tripID) {
		return nil
	}
	key := blockShiftKey{tripID: tripIDs[0], serviceDateUnix: serviceDate.UnixNano()}
	cache := snapshotCacheFrom(ctx)
	if cache == nil {
		return api.preferredLivePositionVehicle(tripIDs, serviceDate)
	}
	// Sibling trips share both the assignment and the resolved shift, avoiding
	// another block/calendar/bounds query for each trip's anonymous update.
	for _, id := range tripIDs {
		cache.blockShiftTripIDs[blockShiftKey{tripID: id, serviceDateUnix: key.serviceDateUnix}] = tripIDs
	}
	if cache.positionVehiclesByShift == nil {
		cache.positionVehiclesByShift = make(map[blockShiftKey]*gtfs.Vehicle)
	}
	vehicle, found := cache.positionVehiclesByShift[key]
	if !found {
		vehicle = api.preferredLivePositionVehicle(tripIDs, serviceDate)
		cache.positionVehiclesByShift[key] = vehicle
	}
	return vehicle
}

func (api *RestAPI) preferredLivePositionVehicle(tripIDs []string, serviceDate time.Time) *gtfs.Vehicle {
	trips := make(map[string]bool, len(tripIDs))
	for _, id := range tripIDs {
		trips[id] = true
	}
	seen := make(map[string]bool)
	var preferred *gtfs.Vehicle
	for _, vehicle := range api.GtfsManager.GetLiveRealTimeVehicles() {
		if vehicle.ID == nil || vehicle.ID.ID == "" || vehicle.Trip == nil || isTripUpdateVehicle(&vehicle) {
			continue
		}
		// Java accepts the first position for each vehicle before assigning
		// blocks, even if the feed repeats that vehicle on another trip.
		if seen[vehicle.ID.ID] {
			continue
		}
		seen[vehicle.ID.ID] = true
		if trips[vehicle.Trip.ID.ID] && tripUpdateOnServiceDate(*vehicle.Trip, serviceDate) {
			preferred = &vehicle
		}
	}
	return preferred
}
