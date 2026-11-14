package restapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// reportProblemWithTripHandler accepts a user-submitted problem report for a specific trip
// and persists it to the database.
func (api *RestAPI) reportProblemWithTripHandler(w http.ResponseWriter, r *http.Request) {
	reqLogger := logging.ForComponent(r.Context(), "problem_reporting")
	agencyID, tripID, ok := api.extractAgencyCodeID(w, r, "tripId")
	if !ok {
		return
	}
	// The raw GTFS trip ID (e.g., "t_123")
	compositeID := utils.FormCombinedID(agencyID, tripID) // The API ID (e.g., "1_t_123")

	query := r.URL.Query()

	var fieldErrors map[string][]string
	parsedServiceDate, fieldErrors := utils.ParseInt64Param(query, "serviceDate", fieldErrors)
	userLat, fieldErrors := utils.ParseFloatParam(query, "userLat", fieldErrors)
	userLon, fieldErrors := utils.ParseFloatParam(query, "userLon", fieldErrors)
	userLocationAccuracy, fieldErrors := utils.ParseFloatParam(query, "userLocationAccuracy", fieldErrors)
	userOnVehicle, fieldErrors := utils.ParseBoolParam(query, "userOnVehicle", false, fieldErrors)
	fieldErrors = validateUserCoordinates(query, userLat, userLon, fieldErrors)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		reqLogger.Error("report problem with trip failed: GTFS DB not initialized")
		api.serverErrorResponse(w, r, errors.New("GTFS DB not initialized"))
		return
	}

	var serviceDateStr string
	if parsedServiceDate != nil {
		serviceDateStr = strconv.FormatInt(*parsedServiceDate, 10)
	}

	vehicleID := query.Get("vehicleId")
	stopID := query.Get("stopId")
	code := query.Get("code")
	userComment := utils.TruncateComment(query.Get("userComment"))
	userVehicleNumber := query.Get("userVehicleNumber")

	// Log the problem report for observability
	reqLogger.Info("problem_report_received_for_trip",
		"trip_id", tripID,
		"composite_id", compositeID,
		"code", code,
		"service_date", serviceDateStr,
		"vehicle_id", vehicleID,
		"stop_id", stopID)

	onVehicleValue := int64(0)
	if userOnVehicle {
		onVehicleValue = 1
	}

	// Store the problem report in the database
	now := api.Clock.Now().UnixMilli()
	params := gtfsdb.CreateProblemReportTripParams{
		TripID:               tripID,
		ServiceDate:          nulls.NonEmptyString(serviceDateStr),
		VehicleID:            nulls.NonEmptyString(vehicleID),
		StopID:               nulls.NonEmptyString(stopID),
		Code:                 nulls.NonEmptyString(code),
		UserComment:          nulls.NonEmptyString(userComment),
		UserLat:              nullableReportFloat(userLat, query.Get("userLat")),
		UserLon:              nullableReportFloat(userLon, query.Get("userLon")),
		UserLocationAccuracy: nullableReportFloat(userLocationAccuracy, query.Get("userLocationAccuracy")),
		UserOnVehicle:        sql.NullInt64{Int64: onVehicleValue, Valid: query.Get("userOnVehicle") != ""},
		UserVehicleNumber:    nulls.NonEmptyString(userVehicleNumber),
		CreatedAt:            now,
		SubmittedAt:          now,
	}

	err := api.GtfsManager.GtfsDB.Queries.CreateProblemReportTrip(r.Context(), params)
	if err != nil {
		reqLogger.Error("failed to store problem report", "error", err,
			"trip_id", tripID)
		api.serverErrorResponse(w, r, err)
		return
	}

	api.sendResponse(w, r, models.NewOKResponse(struct{}{}, api.Clock))
}
