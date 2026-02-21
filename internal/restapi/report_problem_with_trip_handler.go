package restapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// ReportProblemTrip represents a trip-related issue report submitted by a user.
type ReportProblemTrip struct {
	CompositeID          string   `json:"composite_id"`
	ServiceDate          string   `json:"service_date,omitempty"`
	VehicleID            string   `json:"vehicle_id,omitempty"`
	StopID               string   `json:"stop_id,omitempty"`
	Code                 string   `json:"code,omitempty"`
	UserComment          string   `json:"user_comment,omitempty"`
	UserVehicleNumber    string   `json:"user_vehicle_number,omitempty"`
	UserOnVehicle        string   `json:"user_on_vehicle,omitempty"`
	UserLat              *float64 `json:"user_lat,omitempty"`
	UserLon              *float64 `json:"user_lon,omitempty"`
	UserLocationAccuracy *float64 `json:"user_location_accuracy,omitempty"`
}

func (api *RestAPI) reportProblemWithTripHandler(w http.ResponseWriter, r *http.Request) {
	logger := api.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var repProbTrip ReportProblemTrip
	if err := dec.Decode(&repProbTrip); err != nil {
		logger.Error("report problem with trip failed: Error decoding json")
		http.Error(w, `{"code":400, "text":"bad request"}`, http.StatusBadRequest)
		return
	}

	if err := utils.ValidateID(repProbTrip.CompositeID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Extract agency ID and trip ID from composite ID
	_, tripID, err := utils.ExtractAgencyIDAndCodeID(repProbTrip.CompositeID)
	if err != nil {
		logger.Warn("report problem with trip failed: invalid tripID format",
			slog.String("tripID", repProbTrip.CompositeID),
			slog.Any("error", err))
		http.Error(w, `{"code":400, "text":"tripID is required"}`, http.StatusBadRequest)
		return
	}

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		logger.Error("report problem with trip failed: GTFS DB not initialized")
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	userComment := utils.TruncateComment(repProbTrip.UserComment)

	// Log the problem report for observability
	logger = logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(logger, "problem_report_received_for_trip",
		slog.String("trip_id", tripID),
		slog.String("code", repProbTrip.Code),
		slog.String("service_date", repProbTrip.ServiceDate),
		slog.String("vehicle_id", repProbTrip.VehicleID),
		slog.String("stop_id", repProbTrip.StopID),
		slog.String("user_comment", userComment),
		slog.String("user_vehicle_number", repProbTrip.UserVehicleNumber),

		slog.Any("user_on_vehicle", repProbTrip.UserOnVehicle),
		slog.Any("user_lat", repProbTrip.UserLat),
		slog.Any("user_lon", repProbTrip.UserLon),
		slog.Any("user_location_accuracy", repProbTrip.UserLocationAccuracy))

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportTripParams{
		TripID:               tripID,
		ServiceDate:          gtfsdb.ToNullString(repProbTrip.ServiceDate),
		VehicleID:            gtfsdb.ToNullString(repProbTrip.VehicleID),
		StopID:               gtfsdb.ToNullString(repProbTrip.StopID),
		Code:                 gtfsdb.ToNullString(repProbTrip.Code),
		UserComment:          gtfsdb.ToNullString(userComment),
		UserLat:              gtfsdb.Float64ToNull(repProbTrip.UserLat),
		UserLon:              gtfsdb.Float64ToNull(repProbTrip.UserLon),
		UserLocationAccuracy: gtfsdb.Float64ToNull(repProbTrip.UserLocationAccuracy),
		UserOnVehicle:        gtfsdb.ParseNullBool(repProbTrip.UserOnVehicle),
		UserVehicleNumber:    gtfsdb.ToNullString(repProbTrip.UserVehicleNumber),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	err = api.GtfsManager.GtfsDB.Queries.CreateProblemReportTrip(r.Context(), params)
	if err != nil {
		logging.LogError(logger, "failed to store problem report", err,
			slog.String("trip_id", tripID))
		http.Error(w, `{"code":500, "text":"failed to store problem report"}`, http.StatusInternalServerError)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
