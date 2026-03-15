package restapi

import (
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) reportProblemWithTripHandler(w http.ResponseWriter, r *http.Request) {
	logger := api.Logger
	if logger == nil {
		logger = logging.FromContext(r.Context())
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
	logger = logging.FromContext(r.Context()).With("component", "problem_reporting")
	logging.LogOperation(logger, "problem_report_received_for_trip",
		"trip_id", tripID,
		"composite_id", compositeID,
		"code", code,
		"service_date", serviceDate,
		"vehicle_id", vehicleID,
		"stop_id", stopID,
		"user_comment", userComment,
		"user_on_vehicle", userOnVehicle,
		"user_vehicle_number", userVehicleNumber,
		"user_lat", userLatStr,
		"user_lon", userLonStr,
		"user_location_accuracy", userLocationAccuracy)

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
			"trip_id", tripID)
		http.Error(w, `{"code":500, "text":"failed to store problem report"}`, http.StatusInternalServerError)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
