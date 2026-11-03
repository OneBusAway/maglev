package restapi

import (
	"context"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/servicedate"
	"maglev.onebusaway.org/internal/utils"
)

// tripUpdateVehicleBlockWindow mirrors Java's _blockInstanceMatchingWindow
// (BlockLocationServiceImpl.java): a record only matches a block running
// within this long of the record's time.
const tripUpdateVehicleBlockWindow = time.Hour

// vehicleForTrip returns the vehicle serving a trip's block: one reporting
// positions when there is one, otherwise one known only from a trip update.
func (api *RestAPI) vehicleForTrip(ctx context.Context, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	if vehicle := api.GtfsManager.GetVehicleForTrip(ctx, tripID); vehicle != nil {
		return vehicle
	}
	return api.tripUpdateVehicleForTrip(ctx, tripID, serviceDate)
}

// tripUpdateVehicleForTrip finds a vehicle named by a trip update on the trip's
// block but missing from the vehicle positions feed. Java groups trip updates
// by vehicle and builds a record even with no position (GtfsRealtimeTripLibrary
// .java:355-378), so feeds that plan a block before a bus is assigned, such as
// Swiftly's block_*_schedBasedVehicle, still produce a vehicle in its responses.
func (api *RestAPI) tripUpdateVehicleForTrip(ctx context.Context, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	for _, blockTripID := range api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate) {
		for _, tripUpdate := range api.GtfsManager.GetTripUpdatesForTrip(blockTripID) {
			if vehicle := api.vehicleFromTripUpdate(ctx, tripUpdate, blockTripID, serviceDate); vehicle != nil {
				return vehicle
			}
		}
	}
	return nil
}

func (api *RestAPI) vehicleFromTripUpdate(ctx context.Context, tripUpdate gtfs.Trip, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	namesVehicle := tripUpdate.Vehicle != nil && tripUpdate.Vehicle.ID != nil && tripUpdate.Vehicle.ID.ID != ""
	if !namesVehicle || !tripUpdateOnServiceDate(tripUpdate, serviceDate) {
		return nil
	}
	if api.tripUpdateExpired(tripUpdate) || api.tripUpdateVehicleHasPosition(tripUpdate) {
		return nil
	}

	recordTime := api.Clock.Now()
	if tripUpdate.Timestamp != nil {
		recordTime = *tripUpdate.Timestamp
	}
	if !api.blockRunsNear(ctx, tripID, recordTime, serviceDate) {
		return nil
	}

	tripUpdate.IsEntityInMessage = true
	return &gtfs.Vehicle{ID: tripUpdate.Vehicle.ID, Trip: &tripUpdate, Timestamp: &recordTime}
}

func tripUpdateOnServiceDate(tripUpdate gtfs.Trip, serviceDate time.Time) bool {
	const yyyymmdd = "20060102"
	return !tripUpdate.ID.HasStartDate || tripUpdate.ID.StartDate.Format(yyyymmdd) == serviceDate.Format(yyyymmdd)
}

// blockRunsNear reports whether the trip's block is scheduled to run within
// tripUpdateVehicleBlockWindow of recordTime.
func (api *RestAPI) blockRunsNear(ctx context.Context, tripID string, recordTime, serviceDate time.Time) bool {
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, recordTime, serviceDate)
	if snap == nil || len(snap.Stops) == 0 {
		return false
	}
	recordSeconds := utils.CalculateSecondsSinceServiceDate(recordTime, serviceDate)
	window := int64(tripUpdateVehicleBlockWindow.Seconds())
	blockStart := snap.Stops[0].ArrivalSeconds
	blockEnd := snap.Stops[len(snap.Stops)-1].DepartureSeconds
	return recordSeconds >= blockStart-window && recordSeconds <= blockEnd+window
}

// isTripUpdateVehicle reports whether a vehicle comes from a trip update alone,
// as built by vehicleFromTripUpdate, rather than from the vehicle positions feed.
func isTripUpdateVehicle(vehicle *gtfs.Vehicle) bool {
	return vehicle != nil &&
		!vehicle.IsEntityInMessage &&
		vehicle.Position == nil &&
		vehicle.Trip != nil &&
		vehicle.Trip.IsEntityInMessage
}

// responseVehicleID renders a vehicle's ID for an API response. Java leaves a
// trip-update-only ID as the feed sent it when it already splits into an agency
// and an ID, so live Tampa reports block_37462_schedBasedVehicle unprefixed.
func responseVehicleID(agencyID string, vehicle *gtfs.Vehicle) string {
	if isTripUpdateVehicle(vehicle) && strings.Contains(vehicle.ID.ID, "_") {
		return vehicle.ID.ID
	}
	return utils.FormCombinedID(agencyID, vehicle.ID.ID)
}

// statusBlockSnapshot positions the block for a trip status at the vehicle's
// schedule time. A vehicle with a position is placed by currentTime; one known
// only from a trip update stays where it was at the update's timestamp, the way
// live Java reports it.
//
// A schedule time before the block starts is held at the block's first stop.
// Java's getScheduledBlockLocationBeforeStartOfBlock does the same however long
// before the start, so distanceFromStop and numberOfStopsAway stay meaningful.
func (api *RestAPI) statusBlockSnapshot(
	ctx context.Context,
	status *models.TripStatus,
	vehicle *gtfs.Vehicle,
	tripID, agencyID string,
	scheduleDeviation int,
	currentTime, serviceDate time.Time,
) *scheduledBlockSnapshot {
	tripUpdateVehicle := isTripUpdateVehicle(vehicle)
	positionTime := currentTime
	if tripUpdateVehicle {
		positionTime = *vehicle.Timestamp
	}
	effectiveTime := positionTime.Add(-time.Duration(scheduleDeviation) * time.Second)

	snap := api.computeScheduledBlockSnapshot(ctx, tripID, effectiveTime, serviceDate)
	if snap == nil {
		return nil
	}
	blockStart := servicedate.OffsetBase(serviceDate).Add(time.Duration(snap.Stops[0].EffectiveStopSeconds) * time.Second)
	beforeBlockStart := effectiveTime.Before(blockStart) && !runsOwnSchedule(vehicle)

	hasNoPosition := vehicle == nil || vehicle.Position == nil
	if tripUpdateVehicle || (beforeBlockStart && hasNoPosition) {
		applySnapshotStops(status, snap, utils.CalculateSecondsSinceServiceDate(effectiveTime, serviceDate), agencyID)
	}
	if beforeBlockStart {
		return api.computeScheduledBlockSnapshot(ctx, tripID, blockStart, serviceDate)
	}
	return snap
}

// runsOwnSchedule reports whether a vehicle's trip departs from the static block
// schedule, as Java's applyDynamicTripUpdatesToRecord assumes for these trips.
func runsOwnSchedule(vehicle *gtfs.Vehicle) bool {
	if vehicle == nil || vehicle.Trip == nil {
		return false
	}
	relationship := vehicle.Trip.ID.ScheduleRelationship
	return relationship == gtfsrt.TripDescriptor_ADDED || relationship == gtfsrt.TripDescriptor_DUPLICATED
}

// applySnapshotStops mirrors Java's getScheduledBlockLocationBetweenStopTimes
// for a vehicle with no position: the next stop is the snapshot's, and the
// closest stop is whichever of it and the previous stop is nearer in time.
func applySnapshotStops(status *models.TripStatus, snap *scheduledBlockSnapshot, effectiveSeconds int64, agencyID string) {
	if snap.NextStopIndex < 0 {
		return
	}
	next := snap.Stops[snap.NextStopIndex]
	nextOffset := next.ArrivalSeconds - effectiveSeconds
	status.NextStop = utils.FormCombinedID(agencyID, next.StopID)
	status.NextStopTimeOffset = int(nextOffset)

	closestStopID, closestOffset := next.StopID, nextOffset
	if snap.NextStopIndex > 0 {
		previous := snap.Stops[snap.NextStopIndex-1]
		if previousOffset := previous.DepartureSeconds - effectiveSeconds; absInt64(previousOffset) < absInt64(nextOffset) {
			closestStopID, closestOffset = previous.StopID, previousOffset
		}
	}
	status.ClosestStop = utils.FormCombinedID(agencyID, closestStopID)
	status.ClosestStopTimeOffset = int(closestOffset)
}
