package restapi

import (
	"log/slog"
	"net/http"
	"strconv"

	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) reportProblemWithStopHandler(w http.ResponseWriter, r *http.Request) {
	logger := api.Logger
	if logger == nil {
		logger = slog.Default()
	}

	compositeID := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(compositeID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Extract agency ID and stop ID from composite ID
	_, stopID, err := utils.ExtractAgencyIDAndCodeID(compositeID)
	if err != nil {
		logger.Warn("report problem with stop failed: invalid stopID format",
			slog.String("stopID", compositeID),
			slog.Any("error", err))
		http.Error(w, `{"code":400, "text":"stopID is required"}`, http.StatusBadRequest)
		return
	}

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil {
		logger.Error("report problem with stop failed: GTFS DB not initialized")
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	query := r.URL.Query()

	code := query.Get("code")
	rawComment := query.Get("userComment")
	userComment := rawComment
	if len(rawComment) > 500 {
		userComment = rawComment[:500] // Truncate if too long
	}
	userLatStr := query.Get("userLat")
	if userLatStr != "" {
		if _, err := strconv.ParseFloat(userLatStr, 64); err != nil {
			logger.Debug("invalid userLat received", slog.String("val", userLatStr))
			userLatStr = ""
		}
	}
	userLonStr := query.Get("userLon")
	if userLonStr != "" {
		if _, err := strconv.ParseFloat(userLonStr, 64); err != nil {
			logger.Debug("invalid userLon received", slog.String("val", userLonStr))
			userLonStr = ""
		}
	}
	userLocationAccuracy := query.Get("userLocationAccuracy")

	// TODO: Add storage logic for the problem report, I leave it as a log statement for now
	opLogger := logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(opLogger, "problem_report_received_for_stop",
		slog.String("stop_id", stopID),
		slog.String("code", code),
		slog.String("user_comment", userComment),
		slog.String("user_lat", userLatStr),
		slog.String("user_lon", userLonStr),
		slog.String("user_location_accuracy", userLocationAccuracy))

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
