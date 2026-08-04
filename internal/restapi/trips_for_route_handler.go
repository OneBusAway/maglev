package restapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForRouteHandler returns all active trips for a route, including their real-time
// status, schedule, and vehicle positions when available.
func (api *RestAPI) tripsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	includeSchedule := r.URL.Query().Get("includeSchedule") != "false"
	includeStatus := r.URL.Query().Get("includeStatus") != "false"
	includeTrip := parseIncludeTrip(r.URL.Query())
	includeReferences := ShouldIncludeReferences(r)

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			references := models.NewEmptyReferences()
			response := models.NewListResponseWithRange([]models.TripsForRouteListEntry{}, *references, false, api.Clock, false)
			api.sendResponse(w, r, response)
			return
		}
		api.serverErrorResponse(w, r, err)
		return
	}

	currentLocation, err := loadAgencyLocation(currentAgency.ID, currentAgency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	timeParam := r.URL.Query().Get("time")
	_, currentTime, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	windows, err := api.serviceDayWindows(ctx, currentTime)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	allLinkedBlocks, nullBlockTrips, err := api.blockCandidatesForRoute(ctx, routeID, windows)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	if len(allLinkedBlocks) == 0 && len(nullBlockTrips) == 0 {
		var references models.ReferencesModel
		if includeReferences {
			references = buildTripReferences(api, ctx, includeTrip, []models.TripsForRouteListEntry{}, []gtfsdb.Stop{}, nil)
		} else {
			references = *models.NewEmptyReferences()
		}
		response := models.NewListResponseWithRange([]models.TripsForRouteListEntry{}, references, false, api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	activeTrips, _ := api.resolveActiveTrips(ctx, allLinkedBlocks, nullBlockTrips, windows)
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	tripIDsSet := make(map[string]bool)
	for _, id := range activeTrips {
		tripIDsSet[id] = true
	}
	var tripIDs []string
	for id := range tripIDsSet {
		tripIDs = append(tripIDs, id)
	}

	var fetchedTrips []gtfsdb.Trip
	if len(tripIDs) > 0 {
		fetchedTrips, err = api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	// Do NOT filter by trip.RouteID here. Java OBA's trips-for-route intentionally
	// returns trips from other routes when they share a block with a requested-route
	// trip, because the UI uses the block context (previous/next trips).
	// See: https://github.com/OneBusAway/onebusaway-application-modules/issues/90
	// and Brian Ferris's 2012 design note on the legacy API group.
	filteredRouteTrips := make(map[string]bool, len(fetchedTrips))
	for _, trip := range fetchedTrips {
		filteredRouteTrips[trip.ID] = true
	}

	tripAgencyMap := make(map[string]string)
	if len(fetchedTrips) > 0 {
		routeIDSet := make(map[string]struct{})
		for _, trip := range fetchedTrips {
			routeIDSet[trip.RouteID] = struct{}{}
		}
		routeIDs := make([]string, 0, len(routeIDSet))
		for id := range routeIDSet {
			routeIDs = append(routeIDs, id)
		}

		routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		routeAgencyMap := make(map[string]string, len(routes))
		for _, route := range routes {
			routeAgencyMap[route.ID] = route.AgencyID
		}
		for _, trip := range fetchedTrips {
			if agencyID, ok := routeAgencyMap[trip.RouteID]; ok {
				tripAgencyMap[trip.ID] = agencyID
			}
		}
	}

	todayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	stopIDsMap := make(map[string]bool)

	var result []models.TripsForRouteListEntry
	for _, fetchedTrip := range fetchedTrips {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		tripID := fetchedTrip.ID

		agencyID, ok := tripAgencyMap[tripID]
		if !ok {
			continue
		}

		var schedule *models.TripsSchedule
		var status *models.TripStatus

		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, tripID, agencyID, currentTime, currentLocation)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}

			collectStopIDsFromSchedule(schedule, stopIDsMap)
		}

		// Build status if we have a vehicle (either on this trip or we know block has vehicles)
		if includeStatus {
			var statusErr error
			status, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, todayMidnight, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "trip_id", tripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(r.Context(), tripID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}

	// Include DUPLICATED trips from real-time data.
	// DUPLICATED trips (GTFS-RT schedule_relationship=DUPLICATED) are extra runs of
	// a scheduled trip, each assigned to a different vehicle. They only exist in
	// the real-time feed and have no static DB entry.
	//
	// The trip ID format varies by feed:
	//   - Some feeds append a numeric suffix (e.g., _1083.00060) to the base trip ID
	//   - Others reuse the base trip ID as-is
	//   - Others may use entirely synthetic IDs
	// We try the full trip ID first, then fall back to stripping a numeric suffix.
	duplicatedVehicles := api.GtfsManager.GetDuplicatedVehiclesForRoute(routeID)
	for _, vehicle := range duplicatedVehicles {
		if vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
			continue
		}
		dupTripID := vehicle.Trip.ID.ID

		// Resolve the base trip ID for DB lookups.
		// Try the full ID first; if not found, strip a trailing numeric suffix
		// (e.g., ".00060") that some feeds append to distinguish duplicated runs.
		baseTripID := dupTripID
		if _, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, dupTripID); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				api.Logger.Warn("trips-for-route: failed to resolve DUPLICATED trip ID",
					"dup_trip_id", dupTripID, "error", err)
			}
			stripped := stripNumericSuffix(dupTripID)
			if stripped != dupTripID {
				baseTripID = stripped
			}
		}

		var schedule *models.TripsSchedule
		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, baseTripID, agencyID, currentTime, currentLocation)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}
			collectStopIDsFromSchedule(schedule, stopIDsMap)
		}

		var status *models.TripStatus
		if includeStatus {
			var statusErr error
			status, statusErr = api.BuildTripStatus(ctx, agencyID, baseTripID, &vehicle, todayMidnight, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed for DUPLICATED trip", "trip_id", baseTripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(r.Context(), baseTripID),
			TripId:       utils.FormCombinedID(agencyID, dupTripID),
		}
		result = append(result, entry)

		if !filteredRouteTrips[baseTripID] {
			baseTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, baseTripID)
			if err == nil {
				fetchedTrips = append(fetchedTrips, baseTrip)
				filteredRouteTrips[baseTripID] = true
			}
		}
	}

	if result == nil {
		result = []models.TripsForRouteListEntry{}
	}

	var references models.ReferencesModel
	if includeReferences {
		var stops []gtfsdb.Stop
		if len(stopIDsMap) > 0 {
			stopIDs := make([]string, 0, len(stopIDsMap))
			for stopID := range stopIDsMap {
				stopIDs = append(stopIDs, stopID)
			}
			var err error
			stops, err = api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
			if err != nil {
				api.Logger.Warn("failed to fetch stops for references", "error", err, "count", len(stopIDs))
				stops = []gtfsdb.Stop{}
			}
		}

		references = buildTripReferences(api, ctx, includeTrip, result, stops, fetchedTrips)
	} else {
		references = *models.NewEmptyReferences()
	}
	response := models.NewListResponseWithRange(result, references, false, api.Clock, false)
	api.sendResponse(w, r, response)
}

func collectStopIDsFromSchedule(schedule *models.TripsSchedule, stopIDsMap map[string]bool) {
	if schedule == nil {
		return
	}
	for _, stopTime := range schedule.StopTimes {
		_, stopID, err := utils.ExtractAgencyIDAndCodeID(stopTime.StopID)
		if err == nil {
			stopIDsMap[stopID] = true
		}
	}
}

func buildTripReferences(
	api *RestAPI,
	ctx context.Context,
	includeTrip bool,
	trips []models.TripsForRouteListEntry,
	stops []gtfsdb.Stop,
	preFetchedTrips []gtfsdb.Trip,
) models.ReferencesModel {

	presentTrips := make(map[string]models.Trip)
	presentRoutes := make(map[string]models.Route)

	for _, trip := range preFetchedTrips {
		presentTrips[trip.ID] = models.Trip{
			ID:            trip.ID,
			RouteID:       trip.RouteID,
			ServiceID:     trip.ServiceID,
			TripHeadsign:  trip.TripHeadsign.String,
			TripShortName: trip.TripShortName.String,
			DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
			BlockID:       trip.BlockID.String,
			ShapeID:       trip.ShapeID.String,
		}
		presentRoutes[trip.RouteID] = models.Route{}
	}

	for _, trip := range trips {
		_, tripID, _ := utils.ExtractAgencyIDAndCodeID(trip.GetTripId())
		if _, exists := presentTrips[tripID]; !exists {
			presentTrips[tripID] = models.Trip{}
		}
	}

	for _, entry := range trips {
		if entry.Schedule != nil {
			if entry.Schedule.NextTripId != "" {
				_, nextTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Schedule.NextTripId)
				if err == nil {
					if _, exists := presentTrips[nextTripID]; !exists {
						presentTrips[nextTripID] = models.Trip{}
					}
				}
			}
			if entry.Schedule.PreviousTripId != "" {
				_, prevTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Schedule.PreviousTripId)
				if err == nil {
					if _, exists := presentTrips[prevTripID]; !exists {
						presentTrips[prevTripID] = models.Trip{}
					}
				}
			}
		}

		if entry.Status != nil && entry.Status.ActiveTripID != "" {
			_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Status.ActiveTripID)
			if err == nil {
				if _, exists := presentTrips[activeTripID]; !exists {
					presentTrips[activeTripID] = models.Trip{}
				}
			}
		}
	}

	var tripIDsToFetch []string
	for id, t := range presentTrips {
		if t.ID == "" {
			tripIDsToFetch = append(tripIDsToFetch, id)
		}
	}

	if len(tripIDsToFetch) > 0 {
		extraTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDsToFetch)
		if err != nil {
			logging.LogError(api.Logger, "failed to fetch trips for references", err)
		}

		for _, trip := range extraTrips {
			presentTrips[trip.ID] = models.Trip{
				ID:            trip.ID,
				RouteID:       trip.RouteID,
				ServiceID:     trip.ServiceID,
				TripHeadsign:  trip.TripHeadsign.String,
				TripShortName: trip.TripShortName.String,
				DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
				BlockID:       trip.BlockID.String,
				ShapeID:       trip.ShapeID.String,
			}
			presentRoutes[trip.RouteID] = models.Route{}
		}
	}

	var routeIDsToFetch []string
	for id := range presentRoutes {
		routeIDsToFetch = append(routeIDsToFetch, id)
	}

	presentAgencies := make(map[string]models.AgencyReference)

	if len(routeIDsToFetch) > 0 {
		fetchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDsToFetch)
		if err != nil {
			logging.LogError(api.Logger, "failed to fetch routes for references", err)
		}

		for _, route := range fetchedRoutes {
			presentRoutes[route.ID] = models.NewRoute(
				utils.FormCombinedID(route.AgencyID, route.ID),
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String)

			if _, exists := presentAgencies[route.AgencyID]; !exists {
				agency, err := api.GtfsManager.FindAgency(ctx, route.AgencyID)
				if err != nil {
					logging.LogError(api.Logger, "failed to fetch agency for references", err, slog.String("agency", route.AgencyID))
				}

				if agency != nil {
					presentAgencies[agency.ID] = models.AgencyReferenceFromDatabase(agency)
				}
			}
		}
	}

	stopRouteIDs := make(map[string][]string)
	if len(stops) > 0 {
		stopIDs := make([]string, len(stops))
		for i, s := range stops {
			stopIDs[i] = s.ID
		}
		if rows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs); err == nil {
			for _, row := range rows {
				if rid, ok := row.RouteID.(string); ok {
					stopRouteIDs[row.StopID] = append(stopRouteIDs[row.StopID], rid)
				}
			}
		}
	}

	stopList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIdsString := stopRouteIDs[stop.ID]
		if routeIdsString == nil {
			routeIdsString = []string{}
		}

		direction := models.UnknownValue
		if stop.Direction.Valid && stop.Direction.String != "" {
			direction = stop.Direction.String
		}

		stopList = append(stopList, models.Stop{
			Code:               nulls.StringOrEmpty(stop.Code),
			Direction:          direction,
			ID:                 stop.ID,
			Lat:                stop.Lat,
			Lon:                stop.Lon,
			LocationType:       0,
			Name:               nulls.StringOrEmpty(stop.Name),
			Parent:             "",
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}

	tripsRefList := make([]models.Trip, 0, len(presentTrips))
	if includeTrip {
		for _, trip := range presentTrips {
			// Ensure we have the route to get the Agency ID
			if route, ok := presentRoutes[trip.RouteID]; ok {
				currentAgency := route.AgencyID
				tripsRefList = append(tripsRefList, models.Trip{
					ID:            utils.FormCombinedID(currentAgency, trip.ID),
					RouteID:       utils.FormCombinedID(currentAgency, trip.RouteID),
					ServiceID:     utils.FormCombinedID(currentAgency, trip.ServiceID),
					TripHeadsign:  trip.TripHeadsign,
					TripShortName: trip.TripShortName,
					DirectionID:   trip.DirectionID,
					BlockID:       utils.FormCombinedID(currentAgency, trip.BlockID),
					ShapeID:       utils.FormCombinedID(currentAgency, trip.ShapeID),
					PeakOffPeak:   0,
					TimeZone:      "",
				})
			}
		}
	}

	// Convert maps to slices for response
	routes := make([]models.Route, 0, len(presentRoutes))
	for _, route := range presentRoutes {
		if route.ID != "" {
			routes = append(routes, route)
		}
	}

	agencyList := utils.MapValues(presentAgencies)

	references := models.NewEmptyReferences()
	references.Agencies = agencyList
	references.Routes = routes
	references.Stops = stopList
	references.Trips = tripsRefList
	return *references
}

// stripNumericSuffix removes a trailing ".<digits>" from a trip ID.
// Some GTFS-RT feeds append a numeric suffix to DUPLICATED trip IDs to
// distinguish individual runs (e.g., "LLR_..._1083.00060" -> "LLR_..._1083").
// If the ID has no dot, or the part after the last dot contains non-digits,
// the original string is returned unchanged.
func stripNumericSuffix(tripID string) string {
	idx := strings.LastIndex(tripID, ".")
	if idx == -1 || idx == len(tripID)-1 {
		return tripID
	}
	suffix := tripID[idx+1:]
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return tripID
		}
	}
	return tripID[:idx]
}
