package restapi

	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// reportProblemWithStopHandler accepts a user-submitted problem report for a specific stop
// and persists it to the database.
func (api *RestAPI) reportProblemWithStopHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, stopCode, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}
	stopID := stopCode                                      // The raw GTFS stop ID
	compositeID := utils.FormCombinedID(agencyID, stopCode) // The API ID (e.g., "1_stop123")

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		if api.Logger != nil {
			api.Logger.Error("report problem with stop failed: GTFS DB not initialized")
		}
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	query := r.URL.Query()
	code := query.Get("code")
	userComment := utils.TruncateComment(query.Get("userComment"))
	userLatStr := utils.ValidateNumericParam(query.Get("userLat"))
	userLonStr := utils.ValidateNumericParam(query.Get("userLon"))
	userLocationAccuracy := utils.ValidateNumericParam(query.Get("userLocationAccuracy"))

	// Log the problem report for observability
	logger := logging.FromContext(r.Context()).With("component", "problem_reporting")
	logger.Info("problem_report_received_for_stop",
		"stop_id", stopID,
		"composite_id", compositeID,
		"code", code,
		"user_comment", userComment,
		"user_lat", userLatStr,
		"user_lon", userLonStr,
		"user_location_accuracy", userLocationAccuracy)

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportStopParams{
		StopID:               stopID,
		Code:                 gtfsdb.ToNullString(code),
		UserComment:          gtfsdb.ToNullString(userComment),
		UserLat:              gtfsdb.ParseNullFloat(userLatStr),
		UserLon:              gtfsdb.ParseNullFloat(userLonStr),
		UserLocationAccuracy: gtfsdb.ParseNullFloat(userLocationAccuracy),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	err := api.GtfsManager.GtfsDB.Queries.CreateProblemReportStop(r.Context(), params)
	if err != nil {
		logger.Error("failed to store problem report", "error", err,
			"stop_id", stopID)
		http.Error(w, `{"code":500, "text":"failed to store problem report"}`, http.StatusInternalServerError)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
