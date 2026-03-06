package restapi

import (
	"context"
	"math"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// StaleDetector checks whether a vehicle's real-time data is too old to trust.
//
// Java reference: GtfsRealtimeSource.handleCombinedUpdates()
// onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/realtime/gtfs_realtime/GtfsRealtimeSource.java
//
// The Java implementation removes vehicle records from the active map when their
// last update time is more than 15 minutes in the past. We mirror that threshold
// here so stale vehicles are treated as absent rather than as live vehicles.
type StaleDetector struct {
	threshold time.Duration
}

func NewStaleDetector() *StaleDetector {
	return &StaleDetector{threshold: 15 * time.Minute}
}

func (d *StaleDetector) WithThreshold(threshold time.Duration) *StaleDetector {
	return &StaleDetector{threshold: threshold}
}

func (d *StaleDetector) Check(vehicle *gtfs.Vehicle, currentTime time.Time) bool {
	if vehicle == nil {
		return true
	}
	if vehicle.Timestamp == nil {
		return vehicle.Position == nil
	}
	return currentTime.Sub(*vehicle.Timestamp) > d.threshold
}

var defaultStaleDetector = NewStaleDetector()

// scheduleRelationshipStatus converts a GTFS-RT TripDescriptor_ScheduleRelationship to
// the OBA status string.
//
// Java reference: GtfsRealtimeTripLibrary.java
// onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/realtime/gtfs_realtime/GtfsRealtimeTripLibrary.java
//
// Java calls record.setStatus(blockDescriptor.getScheduleRelationship().toString()), which
// produces the enum name as-is ("SCHEDULED", "CANCELED", "ADDED", "DUPLICATED"). The status
// "default" is only used when no real-time data exists at all (TripStatusBeanServiceImpl line 253).
func scheduleRelationshipStatus(sr gtfs.TripScheduleRelationship) string {
	switch sr {
	case gtfsrt.TripDescriptor_CANCELED:
		return "CANCELED"
	case gtfsrt.TripDescriptor_ADDED:
		return "ADDED"
	case gtfsrt.TripDescriptor_DUPLICATED:
		return "DUPLICATED"
	default:
		// UNSCHEDULED, REPLACEMENT, and DELETED all map to "SCHEDULED" here.
		// This matches the Java OBA behavior where only CANCELED, ADDED, and DUPLICATED
		// receive distinct statuses. A DELETED trip appearing as "SCHEDULED" is
		// intentional: the Java server does not expose a DELETED status to clients.
		return "SCHEDULED"
	}
}

// GetVehicleStatusAndPhase returns the OBA status and phase for a vehicle.
//
// Java reference: VehicleStatusServiceImpl.java (handleVehicleLocationRecord)
// onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/realtime/VehicleStatusServiceImpl.java
//
// The Java implementation does not map directly to GTFS-RT CurrentStatus values.
// Instead, it uses a simple rule: if a vehicle location record has been received,
// the trip is "in_progress"; otherwise it remains "scheduled". The phase is
// determined solely by the presence of the vehicle, not by its GTFS-RT stop status.
// Status comes from the trip's schedule relationship ("SCHEDULED", "CANCELED", "ADDED", "DUPLICATED").
// "default" is only returned when no real-time data exists at all.
func GetVehicleStatusAndPhase(vehicle *gtfs.Vehicle) (status string, phase string) {
	if vehicle == nil {
		// "default" matches the Java OBA behavior. In TripStatusBeanServiceImpl.getBlockLocationAsStatusBean()
		// (line 252-253), status is unconditionally set to "default" first. When no real-time data exists,
		// Java file: onebusaway-transit-data-federation/src/main/java/org/onebusaway/transit_data_federation/impl/beans/TripStatusBeanServiceImpl.java
		return "default", "scheduled"
	}

	sr := gtfsrt.TripDescriptor_SCHEDULED
	if vehicle.Trip != nil {
		sr = vehicle.Trip.ID.ScheduleRelationship
	}
	status = scheduleRelationshipStatus(sr)

	// Java sets phase to IN_PROGRESS whenever a vehicle location record is received,
	// regardless of GTFS-RT CurrentStatus — unless the trip is canceled.
	// For CANCELED trips phase is intentionally left as "" (empty string), matching
	// the Java OBA null-phase behavior for canceled trips.
	if sr != gtfsrt.TripDescriptor_CANCELED {
		phase = "in_progress"
	}

	return status, phase
}

func (api *RestAPI) BuildVehicleStatus(
	ctx context.Context,
	vehicle *gtfs.Vehicle,
	tripID string,
	agencyID string,
	status *models.TripStatusForTripDetails,
	currentTime time.Time,
) {
	if vehicle == nil || defaultStaleDetector.Check(vehicle, currentTime) {
		status.Status, status.Phase = GetVehicleStatusAndPhase(nil)
		return
	}

	var lastUpdateTime int64
	if vehicle.Timestamp != nil {
		lastUpdateTime = api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
		status.LastUpdateTime = utils.Int64Ptr(lastUpdateTime)
	}

	if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
		actualPosition := models.Location{
			Lat: float64(*vehicle.Position.Latitude),
			Lon: float64(*vehicle.Position.Longitude),
		}
		status.LastKnownLocation = actualPosition
		// Position is initially set to the raw GPS position.
		// BuildTripStatus will refine this by projecting it onto the route shape
		// after fetching shape data. Note: getVehicleDistanceAlongShapeContextual
		// makes its own GetShapePointsByTripID call; these two fetches are separate.
		status.Position = actualPosition

		if vehicle.Timestamp != nil {
			status.LastLocationUpdateTime = utils.Int64Ptr(lastUpdateTime)
		}
	}

	if vehicle.Position != nil && vehicle.Position.Bearing != nil {
		obaOrientation := (90 - *vehicle.Position.Bearing)
		if obaOrientation < 0 {
			obaOrientation += 360
		}
		status.Orientation = utils.Float64Ptr(float64(obaOrientation))
		status.LastKnownOrientation = utils.Float64Ptr(float64(obaOrientation))
	}

	status.Status, status.Phase = GetVehicleStatusAndPhase(vehicle)

	if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
		status.ActiveTripID = utils.FormCombinedID(agencyID, vehicle.Trip.ID.ID)
	} else {
		status.ActiveTripID = utils.FormCombinedID(agencyID, tripID)
	}
}

func GetVehicleActiveTripID(vehicle *gtfs.Vehicle) string {
	if vehicle == nil || vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
		return ""
	}

	return vehicle.Trip.ID.ID
}

// projectPositionWithShapePoints projects actualPos onto the nearest segment
// of the given shape, returning nil if no segment is within 200 m.
func projectPositionWithShapePoints(shapePoints []gtfs.ShapePoint, actualPos models.Location) *models.Location {
	if len(shapePoints) < 2 {
		return nil
	}

	minDistance := math.MaxFloat64
	var closestPoint models.Location

	for i := 0; i < len(shapePoints)-1; i++ {
		distance, projectedPoint := projectPointToSegment(
			actualPos.Lat, actualPos.Lon,
			shapePoints[i].Latitude, shapePoints[i].Longitude,
			shapePoints[i+1].Latitude, shapePoints[i+1].Longitude,
		)

		if distance < minDistance {
			minDistance = distance
			closestPoint = projectedPoint
		}
	}

	if minDistance <= 200 {
		return &closestPoint
	}

	return nil
}

func projectPointToSegment(px, py, x1, y1, x2, y2 float64) (float64, models.Location) {
	dist, _, projLat, projLon := projectOntoSegment(px, py, x1, y1, x2, y2)
	return dist, models.Location{Lat: projLat, Lon: projLon}
}

func getCurrentVehicleStopSequence(vehicle *gtfs.Vehicle) *int {
	if vehicle == nil || vehicle.CurrentStopSequence == nil {
		return nil
	}
	val := int(*vehicle.CurrentStopSequence)
	return &val
}
