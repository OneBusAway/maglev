package restapi

import (
	"log/slog"
	"net/http"

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

	// TODO: Add storage logic for the problem report, I leave it as a log statement for now
	logger := logging.FromContext(r.Context())
	logging.LogOperation(logger, "problem_report_received_for_stop",
		slog.String("stop_id", stopID),
		slog.String("code", code),
		slog.String("user_comment", userComment),
		slog.String("user_lat", userLat),
		slog.String("user_lon", userLon),
		slog.String("user_location_accuracy", userLocationAccuracy),
		slog.String("component", "problem_reporting"))

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}))
}
