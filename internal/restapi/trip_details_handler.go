package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	GTFS "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type TripDetailsParams struct {
	ServiceDate     *time.Time
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	Time            *time.Time
}

// parseTripIdDetailsParams parses and validates parameters.
func (api *RestAPI) parseTripIdDetailsParams(r *http.Request) (TripDetailsParams, map[string][]string) {
	params := TripDetailsParams{
		IncludeTrip:     true,
		IncludeSchedule: true,
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

func (api *RestAPI) tripDetailsHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(queryParamID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, tripID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Capture parsing errors
	params, fieldErrors := api.parseTripIdDetailsParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
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

	var schedule *models.Schedule
	var status *models.TripStatusForTripDetails

	if params.IncludeStatus {
		status, _ = api.BuildTripStatus(ctx, agencyID, trip.ID, serviceDate, currentTime)
	}

	if params.IncludeSchedule {
		schedule, err = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, loc)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	tripDetails := &models.TripDetails{
		TripID:       utils.FormCombinedID(agencyID, trip.ID),
		ServiceDate:  serviceDateMillis,
		Schedule:     schedule,
		Frequency:    nil,
		SituationIDs: api.GetSituationIDsForTrip(r.Context(), tripID),
	}

	if status != nil && status.VehicleID != "" {
		tripDetails.Status = status
	}

	references := models.NewEmptyReferences()

	if params.IncludeTrip {
		tripsToInclude := []string{utils.FormCombinedID(agencyID, trip.ID)}

		if params.IncludeSchedule && schedule != nil {
			if schedule.NextTripID != "" {
				tripsToInclude = append(tripsToInclude, schedule.NextTripID)
			}
			if schedule.PreviousTripID != "" {
				tripsToInclude = append(tripsToInclude, schedule.PreviousTripID)
			}
		}

		if params.IncludeStatus && status != nil && status.ActiveTripID != "" {
			tripsToInclude = append(tripsToInclude, status.ActiveTripID)
		}

		referencedTrips, err := api.buildReferencedTrips(ctx, agencyID, tripsToInclude, trip)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		referencedTripsIface := make([]interface{}, len(referencedTrips))
		for i, t := range referencedTrips {
			referencedTripsIface[i] = t
		}
		references.Trips = referencedTripsIface
	}

	calc := GTFS.NewAdvancedDirectionCalculator(api.GtfsManager.GtfsDB.Queries)

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
	references.Agencies = append(references.Agencies, agencyModel)

	if params.IncludeSchedule && schedule != nil {
		stops, err := api.buildStopReferences(ctx, calc, agencyID, schedule.StopTimes)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Stops = stops

		routes, err := api.BuildRouteReference(ctx, agencyID, stops)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		routesIface := make([]interface{}, len(routes))
		for i, route := range routes {
			routesIface[i] = route
		}
		references.Routes = routesIface
	}

	response := models.NewEntryResponse(tripDetails, references, api.Clock)
	api.sendResponse(w, r, response)
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) buildReferencedTrips(ctx context.Context, agencyID string, tripsToInclude []string, mainTrip gtfsdb.Trip) ([]*models.Trip, error) {
	referencedTrips := []*models.Trip{}

	for _, tripID := range tripsToInclude {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		_, refTripID, err := utils.ExtractAgencyIDAndCodeID(tripID)
		if err != nil {
			continue
		}

		if refTripID == mainTrip.ID && len(referencedTrips) > 0 {
			continue
		}

		refTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, refTripID)
		if err != nil {
			continue
		}

		refRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, refTrip.RouteID)
		if err != nil {
			continue
		}

		var blockID string
		if refTrip.BlockID.Valid && refTrip.BlockID.String != "" {
			blockID = utils.FormCombinedID(agencyID, refTrip.BlockID.String)
		}

		refTripModel := &models.Trip{
			ID:             tripID,
			RouteID:        utils.FormCombinedID(agencyID, refTrip.RouteID),
			ServiceID:      utils.FormCombinedID(agencyID, refTrip.ServiceID),
			ShapeID:        utils.FormCombinedID(agencyID, refTrip.ShapeID.String),
			TripHeadsign:   refTrip.TripHeadsign.String,
			TripShortName:  refTrip.TripShortName.String,
			DirectionID:    refTrip.DirectionID.Int64,
			BlockID:        blockID,
			RouteShortName: refRoute.ShortName.String,
			TimeZone:       "",
			PeakOffPeak:    0,
		}

		referencedTrips = append(referencedTrips, refTripModel)
	}

	return referencedTrips, nil
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) buildStopReferences(ctx context.Context, calc *GTFS.AdvancedDirectionCalculator, agencyID string, stopTimes []models.StopTime) ([]models.Stop, error) {
	stopIDSet := make(map[string]bool)
	originalStopIDs := make([]string, 0, len(stopTimes))

	for _, st := range stopTimes {
		_, originalStopID, err := utils.ExtractAgencyIDAndCodeID(st.StopID)
		if err != nil {
			continue
		}

		if !stopIDSet[originalStopID] {
			stopIDSet[originalStopID] = true
			originalStopIDs = append(originalStopIDs, originalStopID)
		}
	}

	if len(originalStopIDs) == 0 {
		return []models.Stop{}, nil
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, originalStopIDs)
	if err != nil {
		return nil, err
	}

	stopMap := make(map[string]gtfsdb.Stop)
	for _, stop := range stops {
		stopMap[stop.ID] = stop
	}

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, originalStopIDs)
	if err != nil {
		return nil, err
	}

	routesByStop := make(map[string][]gtfsdb.Route)
	for _, routeRow := range allRoutes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

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
	}

	modelStops := make([]models.Stop, 0, len(stopTimes))
	processedStops := make(map[string]bool)

	for _, st := range stopTimes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		_, originalStopID, err := utils.ExtractAgencyIDAndCodeID(st.StopID)
		if err != nil {
			continue
		}

		if processedStops[originalStopID] {
			continue
		}
		processedStops[originalStopID] = true

		stop, exists := stopMap[originalStopID]
		if !exists {
			continue
		}

		routesForStop := routesByStop[originalStopID]
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

	return modelStops, nil
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) BuildRouteReference(ctx context.Context, agencyID string, stops []models.Stop) ([]models.Route, error) {

	routeIDSet := make(map[string]bool)
	originalRouteIDs := make([]string, 0)

	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		for _, routeID := range stop.StaticRouteIDs {
			_, originalRouteID, err := utils.ExtractAgencyIDAndCodeID(routeID)
			if err != nil {
				continue
			}

			if !routeIDSet[originalRouteID] {
				routeIDSet[originalRouteID] = true
				originalRouteIDs = append(originalRouteIDs, originalRouteID)
			}
		}
	}

	if len(originalRouteIDs) == 0 {
		return []models.Route{}, nil
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, originalRouteIDs)
	if err != nil {
		return nil, err
	}

	modelRoutes := make([]models.Route, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		routeModel := models.Route{
			ID:                utils.FormCombinedID(agencyID, route.ID),
			AgencyID:          agencyID,
			ShortName:         route.ShortName.String,
			LongName:          route.LongName.String,
			Description:       route.Desc.String,
			Type:              models.RouteType(route.Type),
			URL:               route.Url.String,
			Color:             route.Color.String,
			TextColor:         route.TextColor.String,
			NullSafeShortName: route.ShortName.String,
		}
		modelRoutes = append(modelRoutes, routeModel)
	}

	return modelRoutes, nil
}
