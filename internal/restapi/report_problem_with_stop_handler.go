package restapi

import (
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) reportProblemWithStopHandler(w http.ResponseWriter, r *http.Request) {
	stopID := utils.ExtractIDFromParams(r)

	// TODO: Add required validation
	if stopID == "" {
		api.sendNull(w, r)
		return
	}

	query := r.URL.Query()

	code := query.Get("code")
	userComment := query.Get("userComment")
	userLat := query.Get("userLat")
	userLon := query.Get("userLon")
	userLocationAccuracy := query.Get("userLocationAccuracy")

	// Log the problem report for observability
	logger := logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(logger, "problem_report_received_for_stop",
		slog.String("stop_id", stopID),
		slog.String("code", code),
		slog.String("user_comment", userComment),
		slog.String("user_lat", userLat),
		slog.String("user_lon", userLon),
		slog.String("user_location_accuracy", userLocationAccuracy))

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportStopParams{
		StopID:               stopID,
		Code:                 gtfsdb.ToNullString(code),
		UserComment:          gtfsdb.ToNullString(userComment),
		UserLat:              gtfsdb.ParseNullFloat(userLat),
		UserLon:              gtfsdb.ParseNullFloat(userLon),
		UserLocationAccuracy: gtfsdb.ParseNullFloat(userLocationAccuracy),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	// Store in database if available (gracefully handle missing schema)
	if api.GtfsManager != nil && api.GtfsManager.GtfsDB != nil && api.GtfsManager.GtfsDB.Queries != nil {
		_, err := api.GtfsManager.GtfsDB.Queries.CreateProblemReportStop(r.Context(), params)
		if err != nil {
			logging.LogError(logger, "failed to store problem report", err,
				slog.String("stop_id", stopID))
			// Continue despite storage failure - report was already logged
		}
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
