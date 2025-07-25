package restapi

import (
	"context"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) BuildVehicleStatus(
	ctx context.Context,
	vehicle *gtfs.Vehicle,
	tripID string,
	agencyID string,
	status *models.TripStatusForTripDetails,
) {
	if vehicle == nil {
		status.Phase = "scheduled"
		status.Status = "SCHEDULED"
		return
	}

	if vehicle.Timestamp != nil {
		timestampMs := vehicle.Timestamp.UnixNano() / int64(time.Millisecond)
		status.LastLocationUpdateTime = timestampMs
		status.LastUpdateTime = timestampMs
	}

	if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
		position := models.Location{
			Lat: *vehicle.Position.Latitude,
			Lon: *vehicle.Position.Longitude,
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

	if vehicle.CurrentStatus != nil {
		switch *vehicle.CurrentStatus {
		case 0:
			status.Status = "INCOMING_AT"
			status.Phase = "approaching"
		case 1:
			status.Status = "STOPPED_AT"
			status.Phase = "stopped"
		case 2:
			status.Status = "IN_TRANSIT_TO"
			status.Phase = "in_progress"
		default:
			status.Status = "SCHEDULED"
			status.Phase = "scheduled"
		}
	} else {
		status.Status = "SCHEDULED"
		status.Phase = "scheduled"
	}

	if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
		status.ActiveTripID = utils.FormCombinedID(agencyID, vehicle.Trip.ID.ID)
	} else {
		status.ActiveTripID = utils.FormCombinedID(agencyID, tripID)
	}

	status.Predicted = true

	status.Scheduled = false
}
