package restapi

import (
	"database/sql"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// reportProblemWithStopHandler accepts a user-submitted problem report for a specific stop
// and persists it to the database.
func (api *RestAPI) reportProblemWithStopHandler(w http.ResponseWriter, r *http.Request) {
	reqLogger := logging.ForComponent(r.Context(), "problem_reporting")
	agencyID, stopCode, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}
	stopID := stopCode                                      // The raw GTFS stop ID
	compositeID := utils.FormCombinedID(agencyID, stopCode) // The API ID (e.g., "1_stop123")

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		reqLogger.Error("report problem with stop failed: GTFS DB not initialized")
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	query := r.URL.Query()
	code := query.Get("code")
	userComment := utils.TruncateComment(query.Get("userComment"))
	var fieldErrors map[string][]string
	userLat, fieldErrors := utils.ParseOptionalFloatParam(query, "userLat", nil, fieldErrors)
	userLon, fieldErrors := utils.ParseOptionalFloatParam(query, "userLon", nil, fieldErrors)
	userLocationAccuracy, fieldErrors := utils.ParseOptionalFloatParam(query, "userLocationAccuracy", nil, fieldErrors)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Log the problem report for observability
	reqLogger.Info("problem_report_received_for_stop",
		"stop_id", stopID,
		"composite_id", compositeID,
		"code", code)

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportStopParams{
		StopID:               stopID,
		Code:                 nulls.String(code),
		UserComment:          nulls.String(userComment),
		UserLat:              nullableReportFloat(userLat),
		UserLon:              nullableReportFloat(userLon),
		UserLocationAccuracy: nullableReportFloat(userLocationAccuracy),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	err := api.GtfsManager.GtfsDB.Queries.CreateProblemReportStop(r.Context(), params)
	if err != nil {
		reqLogger.Error("failed to store problem report", "error", err,
			"stop_id", stopID)
		http.Error(w, `{"code":500, "text":"failed to store problem report"}`, http.StatusInternalServerError)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}

func nullableReportFloat(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}
