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
		api.Logger.Warn("report problem with trip failed: missing tripID")
		http.Error(w, `{"code":400,"text":"tripID is required"}`, http.StatusBadRequest)
		return
	}

    // fetch ID from DB.
	_, err := api.GtfsManager.GtfsDB.Queries.GetTrip(r.Context(), tripID)
    if err != nil {
		// we get an error, it means the ID wasn't found.
        api.Logger.Warn("report problem with trip failed: tripID not found", 
            slog.String("tripID", tripID),
            slog.Any("error", err))
        http.Error(w, `{"code":404, "text":"tripID not found"}`, http.StatusNotFound)
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
