package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type TripForVehicleParams struct {
	ServiceDate     *time.Time
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	Time            *time.Time
}

// parseTripForVehicleParams parses and validates parameters.
func (api *RestAPI) parseTripForVehicleParams(r *http.Request) (TripForVehicleParams, map[string][]string) {
	params := TripForVehicleParams{
		IncludeTrip:     true,
		IncludeSchedule: false,
		IncludeStatus:   true,
	}

	fieldErrors := make(map[string][]string)

	// Validate serviceDate
	if serviceDateStr := r.URL.Query().Get("serviceDate"); serviceDateStr != "" {
		if serviceDateMs, err := strconv.ParseInt(serviceDateStr, 10, 64); err == nil {
			serviceDate := time.Unix(serviceDateMs/1000, 0)
			params.ServiceDate = &serviceDate
		} else {
			fieldErrors["serviceDate"] = []string{"must be a valid Unix timestamp in milliseconds"}
		}
	}

	if includeTripStr := r.URL.Query().Get("includeTrip"); includeTripStr != "" {
		if val, err := strconv.ParseBool(includeTripStr); err == nil {
			params.IncludeTrip = val
		} else {
			fieldErrors["includeTrip"] = []string{"must be a boolean value (true/false)"}
		}
	}

	if includeScheduleStr := r.URL.Query().Get("includeSchedule"); includeScheduleStr != "" {
		if val, err := strconv.ParseBool(includeScheduleStr); err == nil {
			params.IncludeSchedule = val
		} else {
			fieldErrors["includeSchedule"] = []string{"must be a boolean value (true/false)"}
		}
	}

	if includeStatusStr := r.URL.Query().Get("includeStatus"); includeStatusStr != "" {
		if val, err := strconv.ParseBool(includeStatusStr); err == nil {
			params.IncludeStatus = val
		} else {
			fieldErrors["includeStatus"] = []string{"must be a boolean value (true/false)"}
		}
	}

	// Validate time
	if timeStr := r.URL.Query().Get("time"); timeStr != "" {
		if timeMs, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
			timeParam := time.Unix(timeMs/1000, 0)
			params.Time = &timeParam
		} else {
			fieldErrors["time"] = []string{"must be a valid Unix timestamp in milliseconds"}
		}
	}

	if len(fieldErrors) > 0 {
		return params, fieldErrors
	}

	return params, nil
}

func (api *RestAPI) tripForVehicleHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(queryParamID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, vehicleID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)

	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	vehicle, err := api.GtfsManager.GetVehicleByID(vehicleID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	// Return 404 when vehicle has no associated trip (idle vehicle)
	// or when the trip ID is empty (avoiding a futile DB lookup)
	if vehicle == nil || vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
		api.Logger.Debug("vehicle has no current trip (idle)",
			"vehicleID", vehicleID, "agencyID", agencyID)
		api.sendNotFound(w, r)
		return
	}

	ctx := r.Context()

	// Capture parsing errors
	params, fieldErrors := api.parseTripForVehicleParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	tripID := vehicle.Trip.ID.ID

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	loc := utils.LoadLocationWithUTCFallBack(agency.Timezone, agency.ID)

	var currentTime time.Time
	if params.Time != nil {
		currentTime = params.Time.In(loc)
	} else {
		currentTime = api.Clock.Now().In(loc)
	}

	var serviceDate time.Time
	if params.ServiceDate != nil {
		serviceDate = *params.ServiceDate
	} else {
		// Use time.Date() to get local midnight, not Truncate() which uses UTC
		y, m, d := currentTime.Date()
		serviceDate = time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	serviceDateMillis := serviceDate.Unix() * 1000

	var status *models.TripStatusForTripDetails
	if params.IncludeStatus {
		var statusErr error
		status, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
		if statusErr != nil {
			api.Logger.Warn("failed to build trip status",
				"tripID", tripID,
				"agencyID", agencyID,
				"error", statusErr)
			// Proceeding with nil status, as it's an optional field
		}
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		// If the trip doesn't exist in our DB (sql.ErrNoRows), return 404 instead of 500
		if errors.Is(err, sql.ErrNoRows) {
			api.Logger.Warn("vehicle references non-existent trip",
				"vehicleID", vehicleID, "tripID", tripID, "agencyID", agencyID)
			api.sendNotFound(w, r)
			return
		}
		api.Logger.Error("database error fetching trip",
			"error", err,
			"tripID", tripID,
			"agencyID", agencyID)
		api.serverErrorResponse(w, r, err)
		return
	}

	var schedule *models.Schedule
	if params.IncludeSchedule {
		var scheduleErr error
		schedule, scheduleErr = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, time.Local)
		if scheduleErr != nil {
			api.Logger.Warn("failed to build trip schedule",
				"tripID", tripID,
				"agencyID", agencyID,
				"error", scheduleErr)
		}
	}

	situationIDs := []string{}

	if status != nil {
		alerts := api.GtfsManager.GetAlertsForTrip(r.Context(), vehicle.Trip.ID.ID)
		for _, alert := range alerts {
			if alert.ID != "" {
				situationIDs = append(situationIDs, alert.ID)
			}
		}
	}

	entry := &models.TripDetails{
		TripID:       tripID,
		ServiceDate:  serviceDateMillis,
		Frequency:    nil,
		Status:       status,
		Schedule:     schedule,
		SituationIDs: situationIDs,
	}

	// Build references
	references := models.NewEmptyReferences()

	agencyModel := models.NewAgencyReference(
		agency.ID,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Lang.String,
		agency.Phone.String,
		agency.Email.String,
		agency.FareUrl.String,
		"",
		false,
	)

	stopIDs := []string{}
	calc := gtfs.NewAdvancedDirectionCalculator(api.GtfsManager.GtfsDB.Queries)

	if status != nil {
		if status.ClosestStop != "" {
			_, closestStopID, err := utils.ExtractAgencyIDAndCodeID(status.ClosestStop)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, closestStopID)
		}
		if status.NextStop != "" {
			_, nextStopID, err := utils.ExtractAgencyIDAndCodeID(status.NextStop)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, nextStopID)
		}
	}
	stops, uniqueRouteMap, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, stopIDs, calc)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references.Stops = stops

	for _, route := range uniqueRouteMap {
		routeModel := models.NewRoute(
			utils.FormCombinedID(agencyID, route.ID),
			agencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String,
			route.ShortName.String,
		)
		references.Routes = append(references.Routes, routeModel)
	}

	references.Agencies = append(references.Agencies, agencyModel)

	if params.IncludeTrip {
		tripRef := models.NewTripReference(
			utils.FormCombinedID(agencyID, trip.ID),
			utils.FormCombinedID(agencyID, trip.RouteID),
			utils.FormCombinedID(agencyID, trip.ServiceID),
			trip.TripHeadsign.String,
			trip.TripShortName.String,
			trip.DirectionID.Int64,
			utils.FormCombinedID(agencyID, trip.BlockID.String),
			utils.FormCombinedID(agencyID, trip.ShapeID.String),
		)
		references.Trips = append(references.Trips, tripRef)
	}

	response := models.NewEntryResponse(entry, references, api.Clock)
	api.sendResponse(w, r, response)
}

// BuildStopReferencesAndRouteIDsForStops builds stop references and collects unique routes for the given stop IDs.
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func BuildStopReferencesAndRouteIDsForStops(api *RestAPI, ctx context.Context, agencyID string, stopIDs []string, calc *gtfs.AdvancedDirectionCalculator) ([]models.Stop, map[string]gtfsdb.GetRoutesForStopsRow, error) {
	if len(stopIDs) == 0 {
		return []models.Stop{}, map[string]gtfsdb.GetRoutesForStopsRow{}, nil
	}

	stopIDSet := make(map[string]struct{})
	uniqueStopIDs := make([]string, 0, len(stopIDs))
	for _, id := range stopIDs {
		if _, exists := stopIDSet[id]; !exists {
			stopIDSet[id] = struct{}{}
			uniqueStopIDs = append(uniqueStopIDs, id)
		}
	}

	stopsDB, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, uniqueStopIDs)
	if err != nil {
		return nil, nil, err
	}
	stopMap := make(map[string]gtfsdb.Stop)
	for _, stop := range stopsDB {
		stopMap[stop.ID] = stop
	}

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, uniqueStopIDs)
	if err != nil {
		return nil, nil, err
	}

	routesByStop := make(map[string][]gtfsdb.Route)
	uniqueRouteMap := make(map[string]gtfsdb.GetRoutesForStopsRow)
	for _, routeRow := range allRoutes {
		route := gtfsdb.Route{
			ID:        routeRow.ID,
			AgencyID:  routeRow.AgencyID,
			ShortName: routeRow.ShortName,
			LongName:  routeRow.LongName,
			Desc:      routeRow.Desc,
			Type:      routeRow.Type,
			Url:       routeRow.Url,
			Color:     routeRow.Color,
			TextColor: routeRow.TextColor,
		}
		routesByStop[routeRow.StopID] = append(routesByStop[routeRow.StopID], route)
		combinedID := utils.FormCombinedID(agencyID, routeRow.ID)
		uniqueRouteMap[combinedID] = routeRow
	}

	modelStops := make([]models.Stop, 0, len(uniqueStopIDs))
	for _, stopID := range uniqueStopIDs {
		stop, exists := stopMap[stopID]
		if !exists {
			continue
		}
		routesForStop := routesByStop[stopID]
		combinedRouteIDs := make([]string, len(routesForStop))
		for i, rt := range routesForStop {
			combinedRouteIDs[i] = utils.FormCombinedID(agencyID, rt.ID)
		}
		stopModel := models.Stop{
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Name:               stop.Name.String,
			Lat:                stop.Lat,
			Lon:                stop.Lon,
			Code:               stop.Code.String,
			Direction:          calc.CalculateStopDirection(ctx, stop.ID, stop.Direction),
			LocationType:       int(stop.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		modelStops = append(modelStops, stopModel)
	}

	return modelStops, uniqueRouteMap, nil
}
