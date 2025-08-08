package restapi

import (
	"context"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// GetVehicleStatusAndPhase returns status and phase based on GTFS-RT CurrentStatus
func GetVehicleStatusAndPhase(vehicle *gtfs.Vehicle) (status string, phase string) {
	if vehicle == nil || vehicle.CurrentStatus == nil {
		return "SCHEDULED", "scheduled"
	}

	switch *vehicle.CurrentStatus {
	case 0: // INCOMING_AT
		return "INCOMING_AT", "approaching"
	case 1: // STOPPED_AT
		return "STOPPED_AT", "stopped"
	case 2: // IN_TRANSIT_TO
		return "IN_TRANSIT_TO", "in_progress"
	default:
		return "SCHEDULED", "scheduled"
	}
}

func (api *RestAPI) BuildVehicleStatus(
	ctx context.Context,
	vehicle *gtfs.Vehicle,
	tripID string,
	agencyID string,
	status *models.TripStatusForTripDetails,
) {
	if vehicle == nil {
		status.Status, status.Phase = GetVehicleStatusAndPhase(nil)
		return
	}

	if vehicle.Timestamp != nil {
		timestampMs := vehicle.Timestamp.UnixNano() / int64(time.Millisecond)
		status.LastLocationUpdateTime = timestampMs
		status.LastUpdateTime = timestampMs
	}

	if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
		position := models.Location{
			Lat: float64(*vehicle.Position.Latitude),
			Lon: float64(*vehicle.Position.Longitude),
		}
		status.Position = position
		status.LastKnownLocation = position
	}

	if vehicle.Position != nil && vehicle.Position.Bearing != nil {
		obaOrientation := (90 - *vehicle.Position.Bearing)
		if obaOrientation < 0 {
			obaOrientation += 360
		}
		status.Orientation = float64(obaOrientation)
		status.LastKnownOrientation = float64(obaOrientation)
	}

	status.Status, status.Phase = GetVehicleStatusAndPhase(vehicle)

	if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
		status.ActiveTripID = utils.FormCombinedID(agencyID, vehicle.Trip.ID.ID)
	} else {
		status.ActiveTripID = utils.FormCombinedID(agencyID, tripID)
	}

	status.Predicted = true

	status.Scheduled = false
}
