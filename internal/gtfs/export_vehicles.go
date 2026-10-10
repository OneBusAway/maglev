package gtfs

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
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

// newExportVehicle takes the active trip and block from the first trip update
// naming the vehicle, as legacy grouping does. Legacy never block-matches a
// vehicle position that carries a vehicle ID, so a position-only vehicle
// keeps its own trip descriptor but has no block.
func newExportVehicle(vehicle gtfs.Vehicle, tripByVehicle map[string]tripUpdateRef, tripUpdateBlocks map[string]*BlockMatch) ExportVehicle {
	export := ExportVehicle{Vehicle: vehicle}
	if ref, ok := tripByVehicle[vehicle.ID.ID]; ok {
		export.ActiveTripID = ref.TripID
		export.ActiveRouteID = ref.RouteID
		export.Block = tripUpdateBlocks[ref.TripID]
	} else if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
		export.ActiveTripID = vehicle.Trip.ID.ID
		export.ActiveRouteID = vehicle.Trip.ID.RouteID
	}
	if export.ActiveRouteID == "" && export.Block != nil {
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
		retained := append(slices.Clone(manager.feedVehicles[feedID]), manager.feedFilteredOutVehicles[feedID]...)
		for _, vehicle := range retained {
			// Without an ID a vehicle has no identity for the export to own.
			if vehicle.ID == nil {
				continue
			}
			vehicles = append(vehicles, newExportVehicle(vehicle, tripByVehicle, tripUpdateBlocks))
		}
	}
	return vehicles
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
