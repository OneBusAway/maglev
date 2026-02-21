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

// ReportProblemStop represents a stop-related issue report submitted by a user.
type ReportProblemStop struct {
	CompositeID          string   `json:"composite_id"`
	Code                 string   `json:"code,omitempty"`
	UserComment          string   `json:"user_comment,omitempty"`
	UserLat              *float64 `json:"user_lat,omitempty"`
	UserLon              *float64 `json:"user_lon,omitempty"`
	UserLocationAccuracy *float64 `json:"user_location_accuracy,omitempty"`
}

func (api *RestAPI) reportProblemWithStopHandler(w http.ResponseWriter, r *http.Request) {
	logger := api.Logger
	if logger == nil {
		logger = slog.Default()
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var repProbStop ReportProblemStop
	if err := dec.Decode(&repProbStop); err != nil {
		logger.Error("report problem with stop failed: Error decoding json")
		http.Error(w, `{"code":400, "text":"bad request"}`, http.StatusBadRequest)
		return
	}

	if err := utils.ValidateID(repProbStop.CompositeID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Extract agency ID and stop ID from composite ID
	_, stopID, err := utils.ExtractAgencyIDAndCodeID(repProbStop.CompositeID)
	if err != nil {
		logger.Warn("report problem with stop failed: invalid stopID format",
			slog.String("stopID", repProbStop.CompositeID),
			slog.Any("error", err))
		http.Error(w, `{"code":400, "text":"stopID is required"}`, http.StatusBadRequest)
		return
	}

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		logger.Error("report problem with stop failed: GTFS DB not initialized")
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	userComment := utils.TruncateComment(repProbStop.UserComment)

	// Log the problem report for observability
	logger = logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(logger, "problem_report_received_for_stop",
		slog.String("stop_id", stopID),
		slog.String("code", repProbStop.Code),
		slog.String("user_comment", userComment),
		slog.Any("user_lat", repProbStop.UserLat),
		slog.Any("user_lon", repProbStop.UserLon),
		slog.Any("user_location_accuracy", repProbStop.UserLocationAccuracy))

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportStopParams{
		StopID:               stopID,
		Code:                 gtfsdb.ToNullString(repProbStop.Code),
		UserComment:          gtfsdb.ToNullString(userComment),
		UserLat:              gtfsdb.Float64ToNull(repProbStop.UserLat),
		UserLon:              gtfsdb.Float64ToNull(repProbStop.UserLon),
		UserLocationAccuracy: gtfsdb.Float64ToNull(repProbStop.UserLocationAccuracy),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	err = api.GtfsManager.GtfsDB.Queries.CreateProblemReportStop(r.Context(), params)
	if err != nil {
		logging.LogError(logger, "failed to store problem report", err,
			slog.String("stop_id", stopID))
		http.Error(w, `{"code":500, "text":"failed to store problem report"}`, http.StatusInternalServerError)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
