package restapi

import (
	"context"
	"math"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// GetVehicleStatusAndPhase returns status and phase based on GTFS-RT CurrentStatus
func GetVehicleStatusAndPhase(vehicle *gtfs.Vehicle) (status string, phase string) {
	if vehicle == nil {
		return "SCHEDULED", "scheduled"
	}

	if vehicle.CurrentStatus != nil {
		return "SCHEDULED", "in_progress"
	}

	return "SCHEDULED", "scheduled"
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
		status.LastUpdateTime = api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
	}

	if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
		actualPosition := models.Location{
			Lat: float64(*vehicle.Position.Latitude),
			Lon: float64(*vehicle.Position.Longitude),
		}
		status.LastKnownLocation = actualPosition

		projectedPosition := api.projectPositionOntoRoute(ctx, tripID, actualPosition)
		if projectedPosition != nil {
			status.Position = *projectedPosition
		} else {
			status.Position = actualPosition
		}

		if vehicle.Timestamp != nil {
			status.LastLocationUpdateTime = api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
		}
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

func GetVehicleActiveTripID(vehicle *gtfs.Vehicle) string {
	if vehicle == nil || vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
		return ""
	}

	return vehicle.Trip.ID.ID
}

func getCurrentVehicleStopSequence(vehicle *gtfs.Vehicle) *int {
	if vehicle == nil || vehicle.CurrentStopSequence == nil {
		return nil
	}
	val := int(*vehicle.CurrentStopSequence)
	return &val
}

func (api *RestAPI) projectPositionOntoRoute(ctx context.Context, tripID string, actualPos models.Location) *models.Location {
	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if err != nil || len(shapeRows) < 2 {
		return nil
	}

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{
			Latitude:  sp.Lat,
			Longitude: sp.Lon,
		}
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
	dx := x2 - x1
	dy := y2 - y1

	if dx == 0 && dy == 0 {
		dist := utils.Distance(px, py, x1, y1)
		return dist, models.Location{Lat: x1, Lon: y1}
	}

	t := ((px-x1)*dx + (py-y1)*dy) / (dx*dx + dy*dy)

	if t < 0 {
		dist := utils.Distance(px, py, x1, y1)
		return dist, models.Location{Lat: x1, Lon: y1}
	}
	if t > 1 {
		dist := utils.Distance(px, py, x2, y2)
		return dist, models.Location{Lat: x2, Lon: y2}
	}

	projLat := x1 + t*dx
	projLon := y1 + t*dy

	dist := utils.Distance(px, py, projLat, projLon)
	return dist, models.Location{Lat: projLat, Lon: projLon}
}
