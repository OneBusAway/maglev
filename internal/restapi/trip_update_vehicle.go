package restapi

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
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
		blockUpdates := api.vehicleBlockTripUpdates(ctx, vehicle, serviceDate)
		if !api.vehicleRecordExpired(blockUpdates) {
			return vehicleWithRecordStatus(vehicle, blockUpdates)
		}
	}
	return api.tripUpdateVehicleForTrip(ctx, tripID, serviceDate)
}

// vehicleBlockTripUpdates returns the trip updates of the block a vehicle reports
// serving, which make up Java's record for it. A position-only vehicle has none.
func (api *RestAPI) vehicleBlockTripUpdates(ctx context.Context, vehicle *gtfs.Vehicle, serviceDate time.Time) []tripUpdateForTrip {
	if vehicle.Trip == nil {
		return nil
	}
	tripID := vehicle.Trip.ID.ID
	if len(api.GtfsManager.GetTripUpdatesForTrip(tripID)) == 0 {
		return nil
	}
	blockTripIDs := api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate)
	if !slices.Contains(blockTripIDs, tripID) {
		blockTripIDs = []string{tripID}
	}
	return api.blockTripUpdates(blockTripIDs)
}

// vehicleRecordExpired reports whether Java has dropped a vehicle's record because
// its trip updates are too old. Maglev retains a vehicle that left the feed for
// longer than that, and a position-only vehicle has no record to expire.
func (api *RestAPI) vehicleRecordExpired(blockUpdates []tripUpdateForTrip) bool {
	recordTime, hasTime := blockRecordTime(blockUpdates)
	return hasTime && api.recordTimeExpired(recordTime)
}

// vehicleWithRecordStatus gives a vehicle the status of its record. Java takes it
// from the block's first trip update, and a position only adds a location
// (GtfsRealtimeTripLibrary.java:325, :1101-1102), so an update canceling the trip
// outranks the position's own descriptor. It returns a copy because vehicles are
// shared across requests.
func vehicleWithRecordStatus(vehicle *gtfs.Vehicle, blockUpdates []tripUpdateForTrip) *gtfs.Vehicle {
	if len(blockUpdates) == 0 {
		return vehicle
	}
	firstUpdate := blockUpdates[0].tu
	if firstUpdate.ID.ScheduleRelationship != gtfsrt.TripDescriptor_CANCELED {
		return vehicle
	}
	namesAnotherVehicle := namesVehicle(firstUpdate) &&
		(vehicle.ID == nil || vehicle.ID.ID != firstUpdate.Vehicle.ID.ID)
	if namesAnotherVehicle {
		return vehicle
	}
	canceledTrip := *vehicle.Trip
	canceledTrip.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
	canceledVehicle := *vehicle
	canceledVehicle.Trip = &canceledTrip
	return &canceledVehicle
}

// tripUpdateVehicleForTrip finds a vehicle named by a trip update on the trip's
// block but missing from the vehicle positions feed. Java groups trip updates
// by vehicle and builds a record even with no position (GtfsRealtimeTripLibrary
// .java:355-378), so feeds that plan a block before a bus is assigned, such as
// Swiftly's block_*_schedBasedVehicle, still produce a vehicle in its responses.
//
// The vehicle carries the block's first update, whose schedule relationship is the
// status of Java's whole record (GtfsRealtimeTripLibrary.java:325, :1101-1102).
func (api *RestAPI) tripUpdateVehicleForTrip(ctx context.Context, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	if len(api.GtfsManager.GetAllTripUpdates()) == 0 {
		return nil
	}
	blockTripIDs := api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate)
	blockUpdates := api.collectBlockTripUpdates(blockTripIDs)
	recordTime, hasTime := blockRecordTime(blockUpdates)
	if !hasTime {
		recordTime = api.Clock.Now()
	}
	for _, blockUpdate := range blockUpdates {
		vehicle := api.vehicleFromTripUpdate(ctx, blockUpdate.tu, blockUpdate.tripID, recordTime, serviceDate)
		if vehicle == nil {
			continue
		}
		if !api.recordInService(ctx, vehicle, blockUpdate.tripID, blockTripIDs, recordTime, serviceDate) {
			return nil
		}
		return vehicle
	}
	return nil
}

// recordInService reports whether Java keeps a trip-update vehicle's record. It
// discards one over an hour off schedule (GtfsRealtimeSource.java:728) and has no
// location once the record's schedule time is past the block's last stop, unless
// the trip is canceled (AbstractBlockLocationServiceImpl.java:274). ADDED and
// DUPLICATED trips skip both, since they leave the static block schedule.
func (api *RestAPI) recordInService(ctx context.Context, vehicle *gtfs.Vehicle, tripID string, blockTripIDs []string, recordTime, serviceDate time.Time) bool {
	if runsOwnSchedule(vehicle) {
		return true
	}
	deviation, _, discarded := api.blockRecordDeviation(ctx, blockTripIDs, serviceDate, recordTime)
	if discarded {
		return false
	}
	if isCanceledVehicle(vehicle) {
		return true
	}
	scheduleTime := recordTime.Add(-time.Duration(deviation) * time.Second)
	return !api.pastBlockEnd(ctx, tripID, scheduleTime, serviceDate)
}

func (api *RestAPI) pastBlockEnd(ctx context.Context, tripID string, scheduleTime, serviceDate time.Time) bool {
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, scheduleTime, serviceDate)
	if snap == nil {
		return false
	}
	blockEnd := snap.Stops[len(snap.Stops)-1].DepartureSeconds
	return utils.CalculateSecondsSinceServiceDate(scheduleTime, serviceDate) > blockEnd
}

// tripUpdateVehicleServiceDate returns the service date a vehicle known only from
// a trip update is running tripID on. A trip past midnight belongs to the previous
// date, which is the only case that differs from the date of at.
func (api *RestAPI) tripUpdateVehicleServiceDate(ctx context.Context, tripID string, at time.Time, location *time.Location) servicedate.Date {
	today := servicedate.Of(at)
	yesterday := today.AddDays(-1)
	runsYesterday := api.tripUpdateVehicleForTrip(ctx, tripID, today.Midnight(location)) == nil &&
		api.tripUpdateVehicleForTrip(ctx, tripID, yesterday.Midnight(location)) != nil
	if runsYesterday {
		return yesterday
	}
	return today
}

// reportedTripUpdateVehicle returns the trip's trip-update vehicle when requestedID
// is the ID responses report for it (see responseVehicleID), so that a client can
// send that ID back. It returns nil otherwise.
func (api *RestAPI) reportedTripUpdateVehicle(ctx context.Context, requestedID, agencyID, tripID string, serviceDate time.Time) *gtfs.Vehicle {
	vehicle := api.vehicleForTrip(ctx, tripID, serviceDate)
	if !isTripUpdateVehicle(vehicle) || vehicle.ID == nil || responseVehicleID(agencyID, vehicle) != requestedID {
		return nil
	}
	return vehicle
}

func (api *RestAPI) vehicleFromTripUpdate(ctx context.Context, tripUpdate gtfs.Trip, tripID string, recordTime, serviceDate time.Time) *gtfs.Vehicle {
	if !tripUpdateOnServiceDate(tripUpdate, serviceDate) {
		return nil
	}
	vehicleID := api.tripUpdateVehicleID(ctx, tripUpdate, tripID)
	if vehicleID == nil {
		return nil
	}
	if api.tripUpdateVehicleHasPosition(tripUpdate) {
		return nil
	}

	if !api.blockRunsNear(ctx, tripID, recordTime, serviceDate) {
		return nil
	}

	tripUpdate.IsEntityInMessage = true
	return &gtfs.Vehicle{ID: vehicleID, Trip: &tripUpdate, Timestamp: &recordTime}
}

// tripUpdateVehicleID returns the vehicle a trip update names. Java's record uses
// the block's ID when it names none (GtfsRealtimeTripLibrary.java:556-559).
func (api *RestAPI) tripUpdateVehicleID(ctx context.Context, tripUpdate gtfs.Trip, tripID string) *gtfs.VehicleID {
	if namesVehicle(tripUpdate) {
		return tripUpdate.Vehicle.ID
	}
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		return nil
	}
	blockID := nulls.StringOrEmpty(trip.BlockID)
	if blockID == "" {
		return nil
	}
	return &gtfs.VehicleID{ID: blockID}
}

func namesVehicle(tripUpdate gtfs.Trip) bool {
	return tripUpdate.Vehicle != nil && tripUpdate.Vehicle.ID != nil && tripUpdate.Vehicle.ID.ID != ""
}

func tripUpdateOnServiceDate(tripUpdate gtfs.Trip, serviceDate time.Time) bool {
	const yyyymmdd = "20060102"
	return !tripUpdate.ID.HasStartDate || tripUpdate.ID.StartDate.Format(yyyymmdd) == serviceDate.Format(yyyymmdd)
}

// blockRunsNear reports whether the trip's block is scheduled to run within
// tripUpdateVehicleBlockWindow of recordTime.
func (api *RestAPI) blockRunsNear(ctx context.Context, tripID string, recordTime, serviceDate time.Time) bool {
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, recordTime, serviceDate)
	if snap == nil {
		return false
	}
	recordSeconds := utils.CalculateSecondsSinceServiceDate(recordTime, serviceDate)
	window := int64(tripUpdateVehicleBlockWindow.Seconds())
	blockStart := snap.Stops[0].ArrivalSeconds
	blockEnd := snap.Stops[len(snap.Stops)-1].DepartureSeconds
	return recordSeconds >= blockStart-window && recordSeconds <= blockEnd+window
}

// tripUpdateVehicleTrip finds the trip of a vehicle known only from a trip update
// and the agency that trip belongs to. A raw ID such as
// block_37462_schedBasedVehicle arrives split at its first separator, so its
// parts are joined again to compare it with the feed's.
func (api *RestAPI) tripUpdateVehicleTrip(ctx context.Context, agencyID, vehicleID string) (string, string, bool) {
	rawVehicleID := utils.FormCombinedID(agencyID, vehicleID)
	for _, tripUpdate := range api.GtfsManager.GetAllTripUpdates() {
		if !namesVehicle(tripUpdate) || api.tripUpdateExpired(tripUpdate) {
			continue
		}
		feedVehicleID := tripUpdate.Vehicle.ID.ID
		if feedVehicleID != rawVehicleID && feedVehicleID != vehicleID {
			continue
		}
		tripAgencyID, known := api.tripAgencyID(ctx, tripUpdate.ID.ID)
		if known && (feedVehicleID == rawVehicleID || tripAgencyID == agencyID) {
			return tripAgencyID, api.activeTripOfTripUpdateVehicle(ctx, tripAgencyID, tripUpdate.ID.ID), true
		}
	}
	return api.blockVehicleTrip(ctx, agencyID, vehicleID)
}

// blockVehicleTrip finds the trip of a block standing in as a vehicle because its
// trip update names none.
func (api *RestAPI) blockVehicleTrip(ctx context.Context, agencyID, blockID string) (string, string, bool) {
	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, nulls.String(blockID))
	if err != nil {
		return "", "", false
	}
	for _, blockTrip := range blockTrips {
		for _, tripUpdate := range api.GtfsManager.GetTripUpdatesForTrip(blockTrip.ID) {
			if namesVehicle(tripUpdate) || api.tripUpdateExpired(tripUpdate) {
				continue
			}
			if tripAgencyID, known := api.tripAgencyID(ctx, blockTrip.ID); known && tripAgencyID == agencyID {
				return agencyID, api.activeTripOfTripUpdateVehicle(ctx, agencyID, blockTrip.ID), true
			}
		}
	}
	return "", "", false
}

// activeTripOfTripUpdateVehicle returns the trip the vehicle's block is running
// now. It falls back to tripID when the block has no scheduled position at this
// time.
func (api *RestAPI) activeTripOfTripUpdateVehicle(ctx context.Context, agencyID, tripID string) string {
	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		return tripID
	}
	location, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		return tripID
	}
	now := api.Clock.Now().In(location)
	serviceDate := api.tripUpdateVehicleServiceDate(ctx, tripID, now, location).Midnight(location)
	snap := api.computeScheduledBlockSnapshot(ctx, tripID, now, serviceDate)
	if snap == nil || snap.ActiveTripID == "" {
		return tripID
	}
	return snap.ActiveTripID
}

func (api *RestAPI) tripAgencyID(ctx context.Context, tripID string) (string, bool) {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		return "", false
	}
	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	if err != nil {
		return "", false
	}
	return route.AgencyID, true
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
// and an ID, so live Tampa reports block_37462_schedBasedVehicle unprefixed. A
// block standing in as the vehicle is always qualified with its agency.
func responseVehicleID(agencyID string, vehicle *gtfs.Vehicle) string {
	if isTripUpdateVehicle(vehicle) && namesVehicle(*vehicle.Trip) && strings.Contains(vehicle.ID.ID, "_") {
		return vehicle.ID.ID
	}
	return utils.FormCombinedID(agencyID, vehicle.ID.ID)
}

// isCanceledVehicle reports whether a vehicle's trip is canceled. Java neither
// names the vehicle nor predicts times on such an arrival
// (ArrivalsAndDeparturesBeanServiceImpl.java:426, :449, :476).
func isCanceledVehicle(vehicle *gtfs.Vehicle) bool {
	return vehicle != nil && vehicle.Trip != nil && vehicle.Trip.ID.ScheduleRelationship == gtfsrt.TripDescriptor_CANCELED
}

// statusBlockSnapshot positions the block for a trip status at the vehicle's
// schedule time. Java's record fixes that position when it is built, with
// location interpolation off by default (AbstractBlockLocationServiceImpl.java
// :320), so a vehicle with a trip update stays where its record placed it. A
// vehicle with no record, including a position-only one, is placed by currentTime.
//
// A schedule time before the block starts is held at the block's first stop.
// Java's getScheduledBlockLocationBeforeStartOfBlock does the same however long
// before the start, so distanceFromStop and numberOfStopsAway stay meaningful.
func (api *RestAPI) statusBlockSnapshot(
	ctx context.Context,
	status *models.TripStatus,
	input tripStatusInput,
	blockTripIDs []string,
	scheduleDeviation int,
) *scheduledBlockSnapshot {
	positionTime, hasRecord := api.vehicleRecordTime(input.vehicle, blockTripIDs)
	if !hasRecord {
		positionTime = input.currentTime
	}
	effectiveTime := positionTime.Add(-time.Duration(scheduleDeviation) * time.Second)

	snap := api.computeScheduledBlockSnapshot(ctx, input.dbTripID, effectiveTime, input.serviceDate)
	if snap == nil {
		return nil
	}
	blockStart := servicedate.OffsetBase(input.serviceDate).Add(time.Duration(snap.Stops[0].EffectiveStopSeconds) * time.Second)
	beforeBlockStart := effectiveTime.Before(blockStart) && !runsOwnSchedule(input.vehicle)

	hasNoPosition := input.vehicle == nil || input.vehicle.Position == nil
	if hasRecord || (beforeBlockStart && hasNoPosition) {
		effectiveSeconds := utils.CalculateSecondsSinceServiceDate(effectiveTime, input.serviceDate)
		applySnapshotStops(status, snap, effectiveSeconds, input.agencyID)
	}
	if beforeBlockStart {
		return api.computeScheduledBlockSnapshot(ctx, input.dbTripID, blockStart, input.serviceDate)
	}
	return snap
}

// vehicleRecordTime returns the time of the record Java would hold for a vehicle
// on this block. A position-only vehicle has none, since Java builds records
// from trip updates (see #1915), and neither does one on an ADDED or DUPLICATED
// trip, which leaves the static block schedule.
func (api *RestAPI) vehicleRecordTime(vehicle *gtfs.Vehicle, blockTripIDs []string) (time.Time, bool) {
	if vehicle == nil || runsOwnSchedule(vehicle) {
		return time.Time{}, false
	}
	if recordTime, ok := blockRecordTime(api.collectBlockTripUpdates(blockTripIDs)); ok {
		return recordTime, true
	}
	if isTripUpdateVehicle(vehicle) && vehicle.Timestamp != nil {
		return *vehicle.Timestamp, true
	}
	return time.Time{}, false
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
