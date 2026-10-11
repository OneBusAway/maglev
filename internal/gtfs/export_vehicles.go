package gtfs

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/utils"
)

// ExportVehicle is a retained vehicle plus the ownership evidence the
// GTFS-RT vehicle export needs. Unlike the merged JSON view it includes
// vehicles the agency filter removed.
type ExportVehicle struct {
	Vehicle       gtfs.Vehicle
	ActiveTripID  string
	ActiveRouteID string
	// Block is the legacy-matched block of the active trip; nil when the trip
	// is missing or unresolved.
	Block *BlockMatch
}

// ExportVehicles returns the vehicles retained for the GTFS-RT vehicle
// export. Callers must not mutate the result.
func (manager *Manager) ExportVehicles() []ExportVehicle {
	return manager.mergedRealtime().exportVehicles
}

// tripUpdateBlockMatches resolves every trip-update trip in source order, so
// the first update for a trip supplies its matching reference.
func (manager *Manager) tripUpdateBlockMatches(ctx context.Context, feedID string, refs []tripUpdateRef, feedCreatedAt time.Time) map[string]*BlockMatch {
	matches := make(map[string]*BlockMatch)
	for _, ref := range refs {
		if _, seen := matches[ref.TripID]; seen || ref.TripID == "" {
			continue
		}
		matches[ref.TripID] = manager.matchBlock(ctx, feedID, ref.TripID, tripUpdateMatchingReference(ref, feedCreatedAt))
	}
	return matches
}

// resolveStaticTripRoutes caches the static routes of the trips that
// vehicles and trip updates name, querying only trips not cached yet. A
// failed query leaves those trips on their feed routes for this refresh.
func (manager *Manager) resolveStaticTripRoutes(ctx context.Context, feedID string, refs []tripUpdateRef, vehicles []gtfs.Vehicle) {
	if manager.GtfsDB == nil {
		return
	}
	tripIDs := manager.staticTripRoutes.missing(realtimeTripIDs(refs, vehicles))
	if len(tripIDs) == 0 {
		return
	}
	trips, err := utils.QueryInBatches(ctx, tripIDs, manager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		logging.LogError(logging.ForComponent(ctx, "gtfs_realtime"), "Error loading static routes for realtime trips", err,
			slog.String("feed", feedID))
		return
	}
	manager.staticTripRoutes.store(trips)
}

func realtimeTripIDs(refs []tripUpdateRef, vehicles []gtfs.Vehicle) map[string]struct{} {
	tripIDs := make(map[string]struct{}, len(refs)+len(vehicles))
	for _, ref := range refs {
		if ref.TripID != "" {
			tripIDs[ref.TripID] = struct{}{}
		}
	}
	for _, vehicle := range vehicles {
		if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
			tripIDs[vehicle.Trip.ID.ID] = struct{}{}
		}
	}
	return tripIDs
}

// matchBlock treats a failed matching query as unresolved for this refresh
// only; the matcher does not cache failures.
func (manager *Manager) matchBlock(ctx context.Context, feedID, tripID string, reference time.Time) *BlockMatch {
	if manager.blockMatcher == nil {
		return nil
	}
	match, err := manager.blockMatcher.Match(ctx, feedID, tripID, reference)
	if err != nil {
		logging.LogError(logging.ForComponent(ctx, "gtfs_realtime"), "Error matching realtime trip to block", err,
			slog.String("feed", feedID), slog.String("trip_id", tripID))
		return nil
	}
	return match
}

// vehiclesMissingFrom returns the identified vehicles in all that kept omits.
func vehiclesMissingFrom(all, kept []gtfs.Vehicle) []gtfs.Vehicle {
	keptKeys := make(map[vehicleKey]struct{}, len(kept))
	for _, vehicle := range kept {
		if vehicle.ID != nil {
			keptKeys[newVehicleKey(vehicle.ID)] = struct{}{}
		}
	}
	var missing []gtfs.Vehicle
	for _, vehicle := range all {
		if vehicle.ID == nil {
			continue
		}
		if _, ok := keptKeys[newVehicleKey(vehicle.ID)]; !ok {
			missing = append(missing, vehicle)
		}
	}
	return missing
}

// firstTripUpdateByVehicle maps each vehicle to the first trip update in
// source order that names it, matching legacy grouping by vehicle.
func firstTripUpdateByVehicle(refs []tripUpdateRef) map[string]tripUpdateRef {
	byVehicle := make(map[string]tripUpdateRef)
	for _, ref := range refs {
		if _, seen := byVehicle[ref.VehicleID]; ref.VehicleID == "" || seen {
			continue
		}
		byVehicle[ref.VehicleID] = ref
	}
	return byVehicle
}

// newExportVehicle takes the block from the first trip update naming the
// vehicle, as legacy grouping does; legacy never block-matches a vehicle
// position that carries a vehicle ID. The active trip is the position's own
// trip, because a vehicle's later updates can describe trips further along
// its block; a position without a trip falls back to that first update.
// The active route is that trip's static route, as in legacy, and the
// feed's route only for trips absent from static data.
func newExportVehicle(vehicle gtfs.Vehicle, tripByVehicle map[string]tripUpdateRef, tripUpdateBlocks map[string]*BlockMatch, staticRouteOf func(tripID string) string) ExportVehicle {
	export := ExportVehicle{Vehicle: vehicle}
	ref, hasTripUpdate := tripByVehicle[vehicle.ID.ID]
	if hasTripUpdate {
		export.Block = tripUpdateBlocks[ref.TripID]
	}
	switch {
	case vehicle.Trip != nil && vehicle.Trip.ID.ID != "":
		export.ActiveTripID = vehicle.Trip.ID.ID
		export.ActiveRouteID = vehicle.Trip.ID.RouteID
	case hasTripUpdate:
		export.ActiveTripID = ref.TripID
		export.ActiveRouteID = ref.RouteID
	}
	if staticRouteID := staticRouteOf(export.ActiveTripID); staticRouteID != "" {
		export.ActiveRouteID = staticRouteID
	}
	isBlockTrip := export.Block != nil && export.Block.TripID == export.ActiveTripID
	if export.ActiveRouteID == "" && isBlockTrip {
		export.ActiveRouteID = export.Block.RouteID
	}
	return export
}

// buildExportVehiclesLocked assembles the export view from the per-feed
// maps. The caller holds realTimeMutex.
func (manager *Manager) buildExportVehiclesLocked(feedIDs []string) []ExportVehicle {
	var vehicles []ExportVehicle
	for _, feedID := range feedIDs {
		tripByVehicle := firstTripUpdateByVehicle(manager.feedTripUpdateRefs[feedID])
		tripUpdateBlocks := manager.feedTripUpdateBlocks[feedID]
		for _, vehicle := range manager.retainedExportVehiclesLocked(feedID) {
			vehicles = append(vehicles, newExportVehicle(vehicle, tripByVehicle, tripUpdateBlocks, manager.staticTripRoutes.routeOf))
		}
	}
	return vehicles
}

// retainedExportVehiclesLocked returns one record per identified vehicle of
// a feed. A vehicle the agency filter dropped in the latest refresh can
// still have its earlier on-trip record retained in feedVehicles for
// staleness expiry; the filtered-out record is current, so it wins.
func (manager *Manager) retainedExportVehiclesLocked(feedID string) []gtfs.Vehicle {
	filteredOut := manager.feedFilteredOutVehicles[feedID]
	current := make(map[vehicleKey]struct{}, len(filteredOut))
	for _, vehicle := range filteredOut {
		current[newVehicleKey(vehicle.ID)] = struct{}{}
	}
	retained := make([]gtfs.Vehicle, 0, len(manager.feedVehicles[feedID])+len(filteredOut))
	for _, vehicle := range manager.feedVehicles[feedID] {
		// Without an ID a vehicle has no identity for the export to own.
		if vehicle.ID == nil {
			continue
		}
		if _, superseded := current[newVehicleKey(vehicle.ID)]; !superseded {
			retained = append(retained, vehicle)
		}
	}
	return append(retained, filteredOut...)
}

func (manager *Manager) storeTripUpdateExportStateLocked(feedID string, refs []tripUpdateRef, blocks map[string]*BlockMatch) {
	if manager.feedTripUpdateRefs == nil {
		manager.feedTripUpdateRefs = make(map[string][]tripUpdateRef)
		manager.feedTripUpdateBlocks = make(map[string]map[string]*BlockMatch)
	}
	manager.feedTripUpdateRefs[feedID] = refs
	manager.feedTripUpdateBlocks[feedID] = blocks
}

func (manager *Manager) storeFilteredOutVehiclesLocked(feedID string, filteredOut []gtfs.Vehicle) {
	if manager.feedFilteredOutVehicles == nil {
		manager.feedFilteredOutVehicles = make(map[string][]gtfs.Vehicle)
	}
	manager.feedFilteredOutVehicles[feedID] = filteredOut
}

// assignTripUpdateVehicles gives trip updates that name no vehicle to the
// vehicle running on the same static block, so feeds whose trip updates
// omit vehicle descriptors still group updates by vehicle.
func (manager *Manager) assignTripUpdateVehicles(ctx context.Context, feedID string, refs []tripUpdateRef, vehicles []gtfs.Vehicle) []tripUpdateRef {
	tripIDs := anonymousAssignmentTripIDs(refs, vehicles)
	if len(tripIDs) == 0 || manager.GtfsDB == nil {
		return refs
	}
	trips, err := utils.QueryInBatches(ctx, tripIDs, manager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		logging.LogError(logging.ForComponent(ctx, "gtfs_realtime"), "Error loading blocks for trip update assignment", err,
			slog.String("feed", feedID))
		return refs
	}
	blockKeys := make(map[string]string, len(trips))
	for _, trip := range trips {
		blockKeys[trip.ID] = tripBlockKey(trip)
	}
	return assignAnonymousTripUpdates(refs, vehicles, blockKeys)
}

// anonymousAssignmentTripIDs lists the trips whose blocks assignment needs:
// those of updates without a vehicle, plus those vehicle positions name. It
// is empty when every update already names its vehicle.
func anonymousAssignmentTripIDs(refs []tripUpdateRef, vehicles []gtfs.Vehicle) []string {
	tripIDs := make(map[string]struct{})
	for _, ref := range refs {
		if ref.VehicleID == "" && ref.TripID != "" {
			tripIDs[ref.TripID] = struct{}{}
		}
	}
	if len(tripIDs) == 0 {
		return nil
	}
	for _, vehicle := range vehicles {
		if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
			tripIDs[vehicle.Trip.ID.ID] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(tripIDs))
}

// assignAnonymousTripUpdates ports legacy assignment: an update without a
// vehicle descriptor belongs to the vehicle whose position names a trip on
// the same static block. Legacy lets the last position in the feed win a
// shared block; go-gtfs does not keep position order, so the smallest
// vehicle ID wins instead. refs is not modified.
func assignAnonymousTripUpdates(refs []tripUpdateRef, vehicles []gtfs.Vehicle, blockKeys map[string]string) []tripUpdateRef {
	vehicleByBlock := preferredVehicleByBlock(vehicles, blockKeys)
	assigned := slices.Clone(refs)
	for i, ref := range assigned {
		if ref.VehicleID != "" {
			continue
		}
		if blockKey, ok := blockKeys[ref.TripID]; ok {
			assigned[i].VehicleID = vehicleByBlock[blockKey]
		}
	}
	return assigned
}

func preferredVehicleByBlock(vehicles []gtfs.Vehicle, blockKeys map[string]string) map[string]string {
	vehicleByBlock := make(map[string]string)
	for _, vehicle := range vehicles {
		if vehicle.ID == nil || vehicle.ID.ID == "" || vehicle.Trip == nil {
			continue
		}
		blockKey, ok := blockKeys[vehicle.Trip.ID.ID]
		if !ok {
			continue
		}
		if current, taken := vehicleByBlock[blockKey]; !taken || vehicle.ID.ID < current {
			vehicleByBlock[blockKey] = vehicle.ID.ID
		}
	}
	return vehicleByBlock
}

// tripBlockKey is the static block a trip runs on. A trip without a
// block_id is its own block, as in blockMatcher.
func tripBlockKey(trip gtfsdb.Trip) string {
	if trip.BlockID.Valid && trip.BlockID.String != "" {
		return trip.BlockID.String
	}
	return trip.ID
}
