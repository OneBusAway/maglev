package restapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// TripParams holds the common query parameters for trip-related endpoints
// (trip-details, trip-for-vehicle, etc.).
type TripParams struct {
	ServiceDate     *time.Time
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	Time            *time.Time
}

// parseTripParams parses and validates the common trip query params
// includeScheduleDefault controls the default value of IncludeSchedule when the
// parameter is not present in the request (true for trip-details, false for trip-for-vehicle).
func (api *RestAPI) parseTripParams(r *http.Request, includeScheduleDefault bool, loc ...*time.Location) (TripParams, map[string][]string) {
	params := TripParams{
		IncludeTrip:     true,
		IncludeSchedule: includeScheduleDefault,
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

	// If a timezone location was provided, localize serviceDate and time so that
	// callers receive times in the agency's timezone by default. This prevents the
	// bug where time.Unix(ms/1000, 0) creates a UTC time and downstream
	// Year()/Month()/Day()/Format() calls extract the wrong calendar date for
	// agencies in positive UTC offsets.
	if len(loc) > 0 && loc[0] != nil {
		if params.ServiceDate != nil {
			localized := params.ServiceDate.In(loc[0])
			params.ServiceDate = &localized
		}
		if params.Time != nil {
			localized := params.Time.In(loc[0])
			params.Time = &localized
		}
	}

	return params, nil
}

func (api *RestAPI) tripDetailsHandler(w http.ResponseWriter, r *http.Request) {
	parsed, _ := utils.GetParsedIDFromContext(r.Context())
	agencyID := parsed.AgencyID
	tripID := parsed.CodeID

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

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

	// Parse query params with the agency's timezone so that serviceDate and time
	// are localized at parse time, preventing UTC date-extraction bugs.
	params, fieldErrors := api.parseTripParams(r, true, loc)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	var currentTime time.Time
	if params.Time != nil {
		currentTime = *params.Time
	} else {
		currentTime = api.Clock.Now().In(loc)
	}

	serviceDate, serviceDateMillis := utils.ServiceDateMillis(params.ServiceDate, currentTime)

	var schedule *models.Schedule
	var status *models.TripStatusForTripDetails

	if params.IncludeStatus {
		var statusErr error
		status, statusErr = api.BuildTripStatus(ctx, agencyID, trip.ID, serviceDate, currentTime)
		if statusErr != nil {
			slog.Warn("BuildTripStatus failed",
				slog.String("trip_id", trip.ID),
				slog.String("error", statusErr.Error()))
			status = nil
		}
	}

	if params.IncludeSchedule {
		schedule, err = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, loc)
		if err != nil {
			slog.Warn("BuildTripSchedule failed",
				slog.String("trip_id", trip.ID),
				slog.String("error", err.Error()))
			schedule = nil
		}
	}

	var situationsIDs []string
	if status != nil && len(status.SituationIDs) > 0 {
		situationsIDs = status.SituationIDs
	} else {
		situationsIDs = api.GetSituationIDsForTrip(r.Context(), tripID)
	}

	tripDetails := &models.TripDetails{
		TripID:       utils.FormCombinedID(agencyID, trip.ID),
		ServiceDate:  serviceDateMillis,
		Schedule:     schedule,
		Frequency:    nil,
		SituationIDs: situationsIDs,
	}

	if status != nil {
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

		for _, t := range referencedTrips {
			references.Trips = append(references.Trips, *t)
		}
	}

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

	if len(situationsIDs) > 0 {
		alerts := api.GtfsManager.GetAlertsForTrip(r.Context(), tripID)
		if len(alerts) > 0 {
			situations := api.BuildSituationReferences(alerts)
			references.Situations = append(references.Situations, situations...)
		}
	}

	if params.IncludeSchedule && schedule != nil {
		stops, err := api.buildStopReferences(ctx, agencyID, schedule.StopTimes)
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

		references.Routes = routes
	}

	response := models.NewEntryResponse(tripDetails, references, api.Clock)
	api.sendResponse(w, r, response)
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) buildReferencedTrips(ctx context.Context, agencyID string, tripsToInclude []string, mainTrip gtfsdb.Trip) ([]*models.Trip, error) {
	referencedTrips := []*models.Trip{}

	// extract unique trip IDs for the batch fetch
	uniqueTripIDs := make([]string, 0, len(tripsToInclude))
	seen := make(map[string]bool)
	type tripEntry struct {
		combinedID string
		refTripID  string
	}
	orderedEntries := make([]tripEntry, 0, len(tripsToInclude))

	for _, tripID := range tripsToInclude {
		_, refTripID, err := utils.ExtractAgencyIDAndCodeID(tripID)
		if err != nil {
			continue
		}
		orderedEntries = append(orderedEntries, tripEntry{combinedID: tripID, refTripID: refTripID})
		if !seen[refTripID] {
			seen[refTripID] = true
			uniqueTripIDs = append(uniqueTripIDs, refTripID)
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// batch fetch
	batchedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
	if err != nil {
		return referencedTrips, fmt.Errorf("batch fetch trips: %w", err)
	}

	tripMap := make(map[string]gtfsdb.Trip)
	routeIDSet := make(map[string]bool)
	for _, t := range batchedTrips {
		tripMap[t.ID] = t
		routeIDSet[t.RouteID] = true
	}

	// batch fetch
	routeIDs := make([]string, 0, len(routeIDSet))
	for rid := range routeIDSet {
		routeIDs = append(routeIDs, rid)
	}

	batchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
	if err != nil {
		return referencedTrips, fmt.Errorf("batch fetch routes: %w", err)
	}

	routeMap := make(map[string]gtfsdb.Route)
	for _, rt := range batchedRoutes {
		routeMap[rt.ID] = rt
	}

	for _, entry := range orderedEntries {
		if entry.refTripID == mainTrip.ID && len(referencedTrips) > 0 {
			continue
		}

		refTrip, tripExists := tripMap[entry.refTripID]
		if !tripExists {
			continue
		}

		refRoute, routeExists := routeMap[refTrip.RouteID]
		if !routeExists {
			continue
		}

		var blockID string
		if refTrip.BlockID.Valid && refTrip.BlockID.String != "" {
			blockID = utils.FormCombinedID(agencyID, refTrip.BlockID.String)
		}

		refTripModel := &models.Trip{
			ID:             entry.combinedID,
			RouteID:        utils.FormCombinedID(agencyID, refTrip.RouteID),
			ServiceID:      utils.FormCombinedID(agencyID, refTrip.ServiceID),
			ShapeID:        utils.FormCombinedID(agencyID, refTrip.ShapeID.String),
			TripHeadsign:   refTrip.TripHeadsign.String,
			TripShortName:  refTrip.TripShortName.String,
			DirectionID:    strconv.FormatInt(refTrip.DirectionID.Int64, 10),
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
func (api *RestAPI) buildStopReferences(ctx context.Context, agencyID string, stopTimes []models.StopTime) ([]models.Stop, error) {
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
			Direction:          api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction),
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
