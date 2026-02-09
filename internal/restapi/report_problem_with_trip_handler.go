package restapi

import (
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) reportProblemWithTripHandler(w http.ResponseWriter, r *http.Request) {
	logger := api.Logger
	if logger == nil {
		logger = slog.Default()
	}

	compositeID := utils.ExtractIDFromParams(r)

	if compositeID == "" {
		logger.Warn("report problem with trip failed: missing tripID")
		http.Error(w, `{"code":400, "text":"tripID is required"}`, http.StatusBadRequest)
		return
	}

	// Extract agency ID and trip ID from composite ID
	_, tripID, err := utils.ExtractAgencyIDAndCodeID(compositeID)
	if err != nil {
		logger.Warn("report problem with trip failed: invalid tripID format",
			slog.String("tripID", compositeID),
			slog.Any("error", err))
		http.Error(w, `{"code":400, "text":"tripID is required"}`, http.StatusBadRequest)
		return
	}

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil {
		logger.Error("report problem with trip failed: GTFS DB not initialized")
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
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

	opLogger := logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(opLogger, "problem_report_received_for_trip",
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

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportTripParams{
		TripID:               tripID,
		ServiceDate:          gtfsdb.ToNullString(serviceDate),
		VehicleID:            gtfsdb.ToNullString(vehicleID),
		StopID:               gtfsdb.ToNullString(stopID),
		Code:                 gtfsdb.ToNullString(code),
		UserComment:          gtfsdb.ToNullString(userComment),
		UserLat:              gtfsdb.ParseNullFloat(userLat),
		UserLon:              gtfsdb.ParseNullFloat(userLon),
		UserLocationAccuracy: gtfsdb.ParseNullFloat(userLocationAccuracy),
		UserOnVehicle:        gtfsdb.ParseNullBool(userOnVehicle),
		UserVehicleNumber:    gtfsdb.ToNullString(userVehicleNumber),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	// Store in database if available (gracefully handle missing schema)
	if api.GtfsManager != nil && api.GtfsManager.GtfsDB != nil && api.GtfsManager.GtfsDB.Queries != nil {
		_, err := api.GtfsManager.GtfsDB.Queries.CreateProblemReportTrip(r.Context(), params)
		if err != nil {
			logging.LogError(logger, "failed to store problem report", err,
				slog.String("trip_id", tripID))
			// Continue despite storage failure - report was already logged
		}
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
