package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	gtfsInternal "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) tripsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	id := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(id); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, routeID, err := utils.ExtractAgencyIDAndCodeID(id)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	includeSchedule := r.URL.Query().Get("includeSchedule") != "false"
	includeStatus := r.URL.Query().Get("includeStatus") != "false"

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	currentLocation, err := time.LoadLocation(currentAgency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	timeParam := r.URL.Query().Get("time")
	formattedDate, currentTime, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Calculate nanoseconds since midnight of the service day
	serviceDayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location())
	nanosSinceMidnight := currentTime.Sub(serviceDayMidnight).Nanoseconds()
	if nanosSinceMidnight < 0 {
		nanosSinceMidnight = 0
	}
	currentNanosSinceMidnight := nanosSinceMidnight

	indexIDs, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
		RouteID:    routeID,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	layoverIndices := api.GtfsManager.GetBlockLayoverIndicesForRoute(routeID)

	timeRangeStart := currentNanosSinceMidnight - (10 * 60 * 1_000_000_000)
	timeRangeEnd := currentNanosSinceMidnight + (30 * 60 * 1_000_000_000)

	layoverBlocks := gtfsInternal.GetBlocksInTimeRange(layoverIndices, timeRangeStart, timeRangeEnd)

	if len(indexIDs) == 0 && len(layoverBlocks) == 0 {
		references := buildTripReferences(api, w, r, ctx, includeSchedule, []models.TripsForRouteListEntry{}, []gtfsdb.Stop{})
		response := models.NewListResponseWithRange([]models.TripsForRouteListEntry{}, references, false, api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	allLinkedBlocks := make(map[string]bool)

	if len(indexIDs) > 0 {
		blocksFromIndices, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
			IndexIds:   indexIDs,
			ServiceIds: serviceIDs,
		})
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		for _, b := range blocksFromIndices {
			if b.Valid {
				allLinkedBlocks[b.String] = true
			}
		}
	}

	for _, blockID := range layoverBlocks {
		allLinkedBlocks[blockID] = true
	}

	realTimeVehicles := api.GtfsManager.GetRealTimeVehicles()
	vehiclesByTripID := make(map[string]gtfs.Vehicle)

	for _, vehicle := range realTimeVehicles {
		if vehicle.Position == nil || vehicle.Trip == nil {
			continue
		}
		vehicleTripID := vehicle.Trip.ID.ID
		vehiclesByTripID[vehicleTripID] = vehicle
	}

	type ActiveTripEntry struct {
		TripID     string
		HasVehicle bool
	}
	var activeTrips []ActiveTripEntry

	for blockID := range allLinkedBlocks {
		if ctx.Err() != nil {
			return
		}

		blockIDNullStr := sql.NullString{String: blockID, Valid: true}

		tripsInBlock, err := api.GtfsManager.GtfsDB.Queries.GetTripsInBlock(ctx, gtfsdb.GetTripsInBlockParams{
			BlockID:    blockIDNullStr,
			ServiceIds: serviceIDs,
		})

		if err != nil {
			continue
		}

		activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripInBlockAtTime(ctx, gtfsdb.GetActiveTripInBlockAtTimeParams{
			BlockID:     blockIDNullStr,
			ServiceIds:  serviceIDs,
			CurrentTime: currentNanosSinceMidnight,
		})
		if err != nil {
			continue
		}

		vehiclesInBlock := 0
		for _, tripInBlock := range tripsInBlock {
			if _, hasVehicle := vehiclesByTripID[tripInBlock]; hasVehicle {
				vehiclesInBlock++
			}
		}

		if vehiclesInBlock > 0 {
			for i := 0; i < vehiclesInBlock; i++ {
				activeTrips = append(activeTrips, ActiveTripEntry{
					TripID:     activeTrip,
					HasVehicle: true,
				})
			}

		} else {
			activeTrips = append(activeTrips, ActiveTripEntry{
				TripID:     activeTrip,
				HasVehicle: false,
			})
		}
	}

	tripIDsSet := make(map[string]bool)
	for _, entry := range activeTrips {
		tripIDsSet[entry.TripID] = true
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

	tripRouteMap := make(map[string]string)
	routeIDsSet := make(map[string]bool)
	for _, trip := range fetchedTrips {
		tripRouteMap[trip.ID] = trip.RouteID
		routeIDsSet[trip.RouteID] = true
	}
	var routeIDs []string
	for id := range routeIDsSet {
		routeIDs = append(routeIDs, id)
	}

	var fetchedRoutes []gtfsdb.Route
	if len(routeIDs) > 0 {
		fetchedRoutes, err = api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	routeAgencyMap := make(map[string]string)
	for _, route := range fetchedRoutes {
		routeAgencyMap[route.ID] = route.AgencyID
	}
	tripAgencyMap := make(map[string]string)
	for tripID, rID := range tripRouteMap {
		if aID, ok := routeAgencyMap[rID]; ok {
			tripAgencyMap[tripID] = aID
		}
	}

	todayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	stopIDsMap := make(map[string]bool)

	var result []models.TripsForRouteListEntry
	for _, activeEntry := range activeTrips {
		if ctx.Err() != nil {
			return
		}

		tripID := activeEntry.TripID

		agencyID, ok := tripAgencyMap[tripID]
		if !ok {
			continue
		}

		var schedule *models.TripsSchedule
		var status *models.TripStatusForTripDetails

		if includeSchedule {
			schedule = api.buildScheduleForTrip(ctx, tripID, agencyID, currentTime, currentLocation, w, r)
			if schedule == nil {
				continue
			}

			// Collect stop IDs from this trip's schedule
			if schedule.StopTimes != nil {
				for _, stopTime := range schedule.StopTimes {
					_, stopID, err := utils.ExtractAgencyIDAndCodeID(stopTime.StopID)
					if err == nil {
						stopIDsMap[stopID] = true
					}
				}
			}
		}

		// Build status if we have a vehicle (either on this trip or we know block has vehicles)
		if includeStatus {
			status, _ = api.BuildTripStatus(ctx, agencyID, tripID, currentTime, currentTime)
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

	if result == nil {
		result = []models.TripsForRouteListEntry{}
	}

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

	// Pass only the result list; references function will fetch what it needs
	references := buildTripReferences(api, w, r, ctx, includeSchedule, result, stops)
	response := models.NewListResponseWithRange(result, references, false, api.Clock, false)
	api.sendResponse(w, r, response)
}

// buildTripReferences has been updated to perform efficient batch fetching
func buildTripReferences[T interface{ GetTripId() string }](
	api *RestAPI,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	includeTrip bool,
	trips []T,
	stops []gtfsdb.Stop,
) models.ReferencesModel {

	presentTrips := make(map[string]models.Trip)
	presentRoutes := make(map[string]models.Route)

	for _, trip := range trips {
		_, tripID, _ := utils.ExtractAgencyIDAndCodeID(trip.GetTripId())
		presentTrips[tripID] = models.Trip{}
	}

	for i := range trips {
		tripEntry := trips[i]

		if entry, ok := any(tripEntry).(models.TripsForRouteListEntry); ok {
			if entry.Schedule != nil {
				if entry.Schedule.NextTripId != "" {
					_, nextTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Schedule.NextTripId)
					if err == nil {
						presentTrips[nextTripID] = models.Trip{}
					}
				}
				if entry.Schedule.PreviousTripId != "" {
					_, prevTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Schedule.PreviousTripId)
					if err == nil {
						presentTrips[prevTripID] = models.Trip{}
					}
				}
			}

			if entry.Status != nil && entry.Status.ActiveTripID != "" {
				_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Status.ActiveTripID)
				if err == nil {
					presentTrips[activeTripID] = models.Trip{}
				}
			}
		}
	}

	var tripIDsToFetch []string
	for id := range presentTrips {
		tripIDsToFetch = append(tripIDsToFetch, id)
	}

	if len(tripIDsToFetch) > 0 {
		fetchedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDsToFetch)
		if err != nil {
			api.Logger.Debug("failed to fetch trips for references", "error", err)
		}

		for _, trip := range fetchedTrips {
			presentTrips[trip.ID] = models.Trip{
				ID:            trip.ID,
				RouteID:       trip.RouteID,
				ServiceID:     trip.ServiceID,
				TripHeadsign:  trip.TripHeadsign.String,
				TripShortName: trip.TripShortName.String,
				DirectionID:   trip.DirectionID.Int64,
				BlockID:       trip.BlockID.String,
				ShapeID:       trip.ShapeID.String,
				PeakOffPeak:   0,
				TimeZone:      "",
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
			api.Logger.Debug("failed to fetch routes for references", "error", err)
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
				route.TextColor.String,
				route.ShortName.String,
			)
			// Identify Agency IDs needed
			if _, exists := presentAgencies[route.AgencyID]; !exists {
				currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
				if err == nil {
					presentAgencies[currentAgency.ID] = models.NewAgencyReference(
						currentAgency.ID,
						currentAgency.Name,
						currentAgency.Url,
						currentAgency.Timezone,
						currentAgency.Lang.String,
						currentAgency.Phone.String,
						currentAgency.Email.String,
						currentAgency.FareUrl.String,
						"",
						false,
					)
				}
			}
		}
	}

	stopList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIds, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStop(ctx, stop.ID)
		if err != nil {
			continue
		}

		routeIdsString := make([]string, len(routeIds))
		for i, id := range routeIds {
			rid := id.(string)
			routeIdsString[i] = rid
		}

		direction := models.UnknownValue
		if stop.Direction.Valid && stop.Direction.String != "" {
			direction = stop.Direction.String
		}

		stopList = append(stopList, models.Stop{
			Code:               utils.NullStringOrEmpty(stop.Code),
			Direction:          direction,
			ID:                 stop.ID,
			Lat:                stop.Lat,
			Lon:                stop.Lon,
			LocationType:       0,
			Name:               utils.NullStringOrEmpty(stop.Name),
			Parent:             "",
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}

	tripsRefList := make([]interface{}, 0, len(presentTrips))
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
					BlockID:       trip.BlockID,
					ShapeID:       utils.FormCombinedID(currentAgency, trip.ShapeID),
					PeakOffPeak:   0,
					TimeZone:      "",
				})
			}
		}
	}

	// Convert maps to slices for response
	routes := make([]interface{}, 0, len(presentRoutes))
	for _, route := range presentRoutes {
		if route.ID != "" {
			routes = append(routes, route)
		}
	}

	agencyList := make([]models.AgencyReference, 0, len(presentAgencies))
	for _, agency := range presentAgencies {
		agencyList = append(agencyList, agency)
	}

	return models.ReferencesModel{
		Agencies:   agencyList,
		Routes:     routes,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      stopList,
		Trips:      tripsRefList,
	}
}
