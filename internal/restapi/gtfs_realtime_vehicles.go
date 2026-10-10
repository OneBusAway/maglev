package restapi

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// vehicleExportMaxAgeSeconds is the exclusive freshness limit, compared in
// whole epoch seconds.
const vehicleExportMaxAgeSeconds = 600

// vehicleExportCandidate is a selected vehicle with its resolved identity.
type vehicleExportCandidate struct {
	vehicle   internalgtfs.ExportVehicle
	agencyID  string
	vehicleID string
}

// resolveVehicleOwner applies the legacy vehicle identity rule: the text
// before the first underscore is the agency, even when the source meant the
// whole string as a raw ID. Only an ID without an underscore falls back to
// the matched block's agency. Anything else stays unresolved.
func resolveVehicleOwner(vehicle internalgtfs.ExportVehicle) (string, string, bool) {
	rawID := vehicle.Vehicle.ID.ID
	if agencyID, vehicleID, found := strings.Cut(rawID, "_"); found {
		if agencyID == "" {
			return "", "", false
		}
		return agencyID, vehicleID, true
	}
	if vehicle.Block == nil {
		return "", "", false
	}
	return vehicle.Block.AgencyID, rawID, true
}

// selectExportVehicles returns the requested agency's eligible vehicles,
// ordered by ID so entity numbering is deterministic.
func selectExportVehicles(vehicles []internalgtfs.ExportVehicle, req gtfsRealtimeExportRequest) []vehicleExportCandidate {
	var candidates []vehicleExportCandidate
	for _, vehicle := range vehicles {
		agencyID, vehicleID, ok := resolveVehicleOwner(vehicle)
		if !ok || agencyID != req.AgencyID || !isExportableVehicle(vehicle.Vehicle, req.Time) {
			continue
		}
		if req.RouteFilterID != "" && vehicle.ActiveRouteID != req.RouteFilterID {
			continue
		}
		candidates = append(candidates, vehicleExportCandidate{vehicle: vehicle, agencyID: agencyID, vehicleID: vehicleID})
	}
	slices.SortFunc(candidates, func(a, b vehicleExportCandidate) int {
		return cmp.Compare(a.vehicle.Vehicle.ID.ID, b.vehicle.Vehicle.ID.ID)
	})
	return candidates
}

// isExportableVehicle requires a complete position and an update younger
// than the limit. A future timestamp has negative age and stays eligible.
func isExportableVehicle(vehicle gtfs.Vehicle, requestTime time.Time) bool {
	hasPosition := vehicle.Position != nil &&
		vehicle.Position.Latitude != nil &&
		vehicle.Position.Longitude != nil
	if !hasPosition || vehicle.Timestamp == nil {
		return false
	}
	return requestTime.Unix()-vehicle.Timestamp.Unix() < vehicleExportMaxAgeSeconds
}

// buildVehiclePositionsFeed maps selected vehicles to VehiclePositions with
// sequential entity IDs. routeAgencies resolves trip/route owners when IDs
// keep their agency prefix and no block match supplies one.
func buildVehiclePositionsFeed(candidates []vehicleExportCandidate, req gtfsRealtimeExportRequest, routeAgencies map[string]string) *gtfsrt.FeedMessage {
	feed := newGtfsRealtimeFeed(req.Time)
	for i, candidate := range candidates {
		feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
			Id:      proto.String(strconv.Itoa(i + 1)),
			Vehicle: vehiclePosition(candidate, req.RemoveAgencyIDs, routeAgencies),
		})
	}
	return feed
}

func vehiclePosition(candidate vehicleExportCandidate, removeAgencyIDs bool, routeAgencies map[string]string) *gtfsrt.VehiclePosition {
	vehicle := candidate.vehicle.Vehicle
	tripOwner := activeTripOwner(candidate.vehicle, routeAgencies)
	position := &gtfsrt.VehiclePosition{
		Trip:                activeTripDescriptor(candidate.vehicle, tripOwner, removeAgencyIDs),
		Vehicle:             vehicleDescriptor(candidate, removeAgencyIDs),
		Position:            exportPosition(vehicle.Position),
		CurrentStopSequence: vehicle.CurrentStopSequence,
		CurrentStatus:       vehicle.CurrentStatus,
		Timestamp:           proto.Uint64(uint64(vehicle.Timestamp.Unix())),
		OccupancyStatus:     vehicle.OccupancyStatus,
		OccupancyPercentage: vehicle.OccupancyPercentage,
	}
	if vehicle.StopID != nil {
		position.StopId = proto.String(exportPayloadID(*vehicle.StopID, tripOwner, removeAgencyIDs))
	}
	// go-gtfs stores congestion as a plain value, so UNKNOWN cannot be told
	// apart from absent; do not emit it.
	if vehicle.CongestionLevel != gtfsrt.VehiclePosition_UNKNOWN_CONGESTION_LEVEL {
		position.CongestionLevel = vehicle.CongestionLevel.Enum()
	}
	return position
}

func vehicleDescriptor(candidate vehicleExportCandidate, removeAgencyIDs bool) *gtfsrt.VehicleDescriptor {
	id := candidate.vehicleID
	if !removeAgencyIDs {
		id = utils.FormCombinedID(candidate.agencyID, candidate.vehicleID)
	}
	descriptor := &gtfsrt.VehicleDescriptor{Id: proto.String(id)}
	if label := candidate.vehicle.Vehicle.ID.Label; label != "" {
		descriptor.Label = proto.String(label)
	}
	if plate := candidate.vehicle.Vehicle.ID.LicensePlate; plate != "" {
		descriptor.LicensePlate = proto.String(plate)
	}
	return descriptor
}

func exportPosition(position *gtfs.Position) *gtfsrt.Position {
	return &gtfsrt.Position{
		Latitude:  position.Latitude,
		Longitude: position.Longitude,
		Bearing:   position.Bearing,
		Odometer:  position.Odometer,
		Speed:     position.Speed,
	}
}

// activeTripOwner is the agency of the active trip's route: the block
// match's when it is for that trip, otherwise a static route lookup.
func activeTripOwner(vehicle internalgtfs.ExportVehicle, routeAgencies map[string]string) string {
	if vehicle.Block != nil && vehicle.Block.TripID == vehicle.ActiveTripID {
		return vehicle.Block.AgencyID
	}
	return routeAgencies[vehicle.ActiveRouteID]
}

// activeTripDescriptor describes the vehicle's active trip. An ordinary trip
// with a block match carries the resolved service date and start rather than
// the update's hints; other trips keep their retained descriptor values.
func activeTripDescriptor(vehicle internalgtfs.ExportVehicle, owner string, removeAgencyIDs bool) *gtfsrt.TripDescriptor {
	if vehicle.ActiveTripID == "" {
		return nil
	}
	descriptor := &gtfsrt.TripDescriptor{TripId: proto.String(exportPayloadID(vehicle.ActiveTripID, owner, removeAgencyIDs))}
	if vehicle.ActiveRouteID != "" {
		descriptor.RouteId = proto.String(exportPayloadID(vehicle.ActiveRouteID, owner, removeAgencyIDs))
	}
	retained := retainedTripID(vehicle)
	if retained != nil {
		applyRetainedTripFields(descriptor, *retained)
	}
	if isResolvedOrdinaryTrip(vehicle, retained) {
		descriptor.StartDate = proto.String(vehicle.Block.ServiceDate.Format("20060102"))
		descriptor.StartTime = proto.String(formatGtfsServiceTime(vehicle.Block.TripStart))
	}
	return descriptor
}

// retainedTripID is the vehicle position's own descriptor when it names the
// active trip.
func retainedTripID(vehicle internalgtfs.ExportVehicle) *gtfs.TripID {
	trip := vehicle.Vehicle.Trip
	if trip == nil || trip.ID.ID != vehicle.ActiveTripID {
		return nil
	}
	return &trip.ID
}

func applyRetainedTripFields(descriptor *gtfsrt.TripDescriptor, trip gtfs.TripID) {
	switch trip.DirectionID {
	case gtfs.DirectionID_True:
		descriptor.DirectionId = proto.Uint32(1)
	case gtfs.DirectionID_False:
		descriptor.DirectionId = proto.Uint32(0)
	}
	if trip.HasStartDate {
		descriptor.StartDate = proto.String(trip.StartDate.Format("20060102"))
	}
	if trip.HasStartTime {
		descriptor.StartTime = proto.String(formatGtfsServiceTime(trip.StartTime))
	}
	// Like congestion, go-gtfs stores the relationship as a plain value, so
	// the SCHEDULED default cannot be told apart from absent.
	if trip.ScheduleRelationship != gtfsrt.TripDescriptor_SCHEDULED {
		descriptor.ScheduleRelationship = trip.ScheduleRelationship.Enum()
	}
}

func isResolvedOrdinaryTrip(vehicle internalgtfs.ExportVehicle, retained *gtfs.TripID) bool {
	if vehicle.Block == nil || vehicle.Block.TripID != vehicle.ActiveTripID {
		return false
	}
	return retained == nil || retained.ScheduleRelationship == gtfsrt.TripDescriptor_SCHEDULED
}
