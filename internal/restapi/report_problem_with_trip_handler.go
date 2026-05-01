package restapi

import (
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// reportProblemWithTripHandler accepts a user-submitted problem report for a specific trip
// and persists it to the database.
func (api *RestAPI) reportProblemWithTripHandler(w http.ResponseWriter, r *http.Request) {
	logger := api.Logger
	if logger == nil {
		logger = slog.Default()
	}

	agencyID, tripID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}
	// The raw GTFS trip ID (e.g., "t_123")
	compositeID := utils.FormCombinedID(agencyID, tripID) // The API ID (e.g., "1_t_123")

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		logger.Error("report problem with trip failed: GTFS DB not initialized")
		http.Error(w, `{"code":500, "text":"internal server error"}`, http.StatusInternalServerError)
		return
	}

	query := r.URL.Query()

	serviceDate := query.Get("serviceDate")
	vehicleID := query.Get("vehicleId")
	stopID := query.Get("stopId")
	code := query.Get("code")
	userComment := utils.TruncateComment(query.Get("userComment"))
	userOnVehicle := query.Get("userOnVehicle")
	userVehicleNumber := query.Get("userVehicleNumber")
	userLatStr := utils.ValidateNumericParam(query.Get("userLat"))
	userLonStr := utils.ValidateNumericParam(query.Get("userLon"))
	userLocationAccuracy := utils.ValidateNumericParam(query.Get("userLocationAccuracy"))

	// Log the problem report for observability
	logger = logging.FromContext(r.Context()).With(slog.String("component", "problem_reporting"))
	logging.LogOperation(logger, "problem_report_received_for_trip",
		slog.String("trip_id", tripID),
		slog.String("composite_id", compositeID),
		slog.String("code", code),
		slog.String("service_date", serviceDate),
		slog.String("vehicle_id", vehicleID),
		slog.String("stop_id", stopID))

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportTripParams{
		TripID:               tripID,
		ServiceDate:          gtfsdb.ToNullString(serviceDate),
		VehicleID:            gtfsdb.ToNullString(vehicleID),
		StopID:               gtfsdb.ToNullString(stopID),
		Code:                 gtfsdb.ToNullString(code),
		UserComment:          gtfsdb.ToNullString(userComment),
		UserLat:              gtfsdb.ParseNullFloat(userLatStr),
		UserLon:              gtfsdb.ParseNullFloat(userLonStr),
		UserLocationAccuracy: gtfsdb.ParseNullFloat(userLocationAccuracy),
		UserOnVehicle:        gtfsdb.ParseNullBool(userOnVehicle),
		UserVehicleNumber:    gtfsdb.ToNullString(userVehicleNumber),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	err := api.GtfsManager.GtfsDB.Queries.CreateProblemReportTrip(r.Context(), params)
	if err != nil {
		logging.LogError(logger, "failed to store problem report", err,
			slog.String("trip_id", tripID))
		http.Error(w, `{"code":500, "text":"failed to store problem report"}`, http.StatusInternalServerError)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
