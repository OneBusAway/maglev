package restapi

import (
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) reportProblemWithTripHandler(w http.ResponseWriter, r *http.Request) {

	tripID := utils.ExtractIDFromParams(r)

	// TODO: Add required validation
	if tripID == "" {
		api.sendNull(w, r)
		return
	}

	query := r.URL.Query()

	serviceDate := query.Get("serviceDate")
	vehicleID := query.Get("vehicleId")
	stopID := query.Get("stopId")
	code := query.Get("code")
	userComment := query.Get("userComment")
	userOnVehicle := query.Get("userOnVehicle")
	userVehicleNumber := query.Get("userVehicleNumber")
	userLat := query.Get("userLat")
	userLon := query.Get("userLon")
	userLocationAccuracy := query.Get("userLocationAccuracy")

	// TODO: Add storage logic for the problem report, I leave it as a log statement for now
	logger := logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(logger, "problem_report_received_for_trip",
		slog.String("trip_id", tripID),
		slog.String("code", code),
		slog.String("service_date", serviceDate),
		slog.String("vehicle_id", vehicleID),
		slog.String("stop_id", stopID),
		slog.String("user_comment", userComment),
		slog.String("user_on_vehicle", userOnVehicle),
		slog.String("user_vehicle_number", userVehicleNumber),
		slog.String("user_lat", userLat),
		slog.String("user_lon", userLon),
		slog.String("user_location_accuracy", userLocationAccuracy))

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
