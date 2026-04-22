package restapi

import (
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// scheduleForRouteHandler returns the full schedule for a route on a given date,
// organized by stop-trip groupings with associated service IDs.
func (api *RestAPI) scheduleForRouteHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	dateParam := r.URL.Query().Get("date")
	if err := utils.ValidateDate(dateParam); err != nil {
		fieldErrors := map[string][]string{
			"date": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	ctx := r.Context()

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}
	loc, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	var targetDate string
	var scheduleDate int64
	if dateParam != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", dateParam, loc)
		if err != nil {
			fieldErrors := map[string][]string{
				"date": {"Invalid date format. Use YYYY-MM-DD"},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}
		targetDate = parsedDate.Format("20060102")
		scheduleDate = parsedDate.UnixMilli()
	} else {
		now := api.Clock.Now().In(loc)
		y, m, d := now.Date()
		startOfDay := time.Date(y, m, d, 0, 0, 0, 0, loc)
		targetDate = startOfDay.Format("20060102")
		scheduleDate = startOfDay.UnixMilli()
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, targetDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Behavior Change (Jan 2026): Previously, this returned a 500 Server Error.
	// We now return 200 OK with an empty schedule because "no service found" is a valid state, not a server failure.
	if len(serviceIDs) == 0 {
		entry := models.ScheduleForRouteEntry{
			RouteID:           utils.FormCombinedID(agencyID, routeID),
			ScheduleDate:      scheduleDate,
			ServiceIDs:        []string{},
			StopTripGroupings: []models.StopTripGrouping{},
		}
		api.sendResponse(w, r, models.NewEntryResponse(entry, *models.NewEmptyReferences(), api.Clock))
		return
	}

	trips, err := api.GtfsManager.GtfsDB.Queries.GetTripsForRouteInActiveServiceIDs(ctx, gtfsdb.GetTripsForRouteInActiveServiceIDsParams{
		RouteID:    routeID,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routeSvcIDs := make(map[string]bool)
	combinedServiceIDs := make([]string, 0, len(trips))
	for _, trip := range trips {
		if !routeSvcIDs[trip.ServiceID] {
			routeSvcIDs[trip.ServiceID] = true
			combinedServiceIDs = append(combinedServiceIDs, utils.FormCombinedID(agencyID, trip.ServiceID))
		}
	}

	// Handle case where service exists but this route has no trips today.
	// Return 200 OK with empty data.
	if len(trips) == 0 {
		entry := models.ScheduleForRouteEntry{
			RouteID:           utils.FormCombinedID(agencyID, routeID),
			ScheduleDate:      scheduleDate,
			ServiceIDs:        combinedServiceIDs,
			StopTripGroupings: []models.StopTripGrouping{},
		}
		api.sendResponse(w, r, models.NewEntryResponse(entry, *models.NewEmptyReferences(), api.Clock))
		return
	}

	routeRefs := make(map[string]models.Route)
	tripIDsSet := make(map[string]bool)

	routeModel := models.NewRoute(
		utils.FormCombinedID(agencyID, route.ID),
		route.AgencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String)

	routeRefs[utils.FormCombinedID(agencyID, route.ID)] = routeModel

	dirGroups := groupTripsByDirection(trips)
	var stopTripGroupings []models.StopTripGrouping
	globalStopIDSet := make(map[string]struct{})
	var stopTimesRefs [][]models.RouteStopTime

	for _, group := range dirGroups {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		tripsInGroup := group.Trips

		seenDirSvcIDs := make(map[string]bool)
		var dirServiceIDs []string
		for _, trip := range tripsInGroup {
			if !seenDirSvcIDs[trip.ServiceID] {
				seenDirSvcIDs[trip.ServiceID] = true
				dirServiceIDs = append(dirServiceIDs, trip.ServiceID)
			}
		}

		var orderedStopIDs []string
		var err error
		if !group.DirectionID.Valid {
			orderedStopIDs, err = api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, tripsInGroup[0].ID)
		} else {
			orderedStopIDs, err = api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForRouteDirection(ctx,
				gtfsdb.GetOrderedStopIDsForRouteDirectionParams{
					RouteID:     routeID,
					DirectionID: group.DirectionID,
					ServiceIds:  dirServiceIDs,
				})
		}
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		for _, stopID := range orderedStopIDs {
			globalStopIDSet[stopID] = struct{}{}
		}

		seenHeadsigns := make(map[string]bool)
		var headsigns []string
		for _, trip := range tripsInGroup {
			hs := trip.TripHeadsign.String
			if hs != "" && !seenHeadsigns[hs] {
				seenHeadsigns[hs] = true
				headsigns = append(headsigns, hs)
			}
		}

		rawTripIDs := make([]string, 0, len(tripsInGroup))
		for _, trip := range tripsInGroup {
			rawTripIDs = append(rawTripIDs, trip.ID)
		}

		allStopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, rawTripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		stopTimesByTrip := make(map[string][]gtfsdb.StopTime, len(tripsInGroup))
		for _, st := range allStopTimes {
			stopTimesByTrip[st.TripID] = append(stopTimesByTrip[st.TripID], st)
		}

		var tripIDs []string
		var tripsWithStopTimes []models.TripStopTimes
		for _, trip := range tripsInGroup {
			stopTimes := stopTimesByTrip[trip.ID]
			if len(stopTimes) == 0 {
				continue
			}
			combinedTripID := utils.FormCombinedID(agencyID, trip.ID)
			tripIDsSet[trip.ID] = true
			tripIDs = append(tripIDs, combinedTripID)

			stopTimesList := make([]models.RouteStopTime, 0, len(stopTimes))
			for _, st := range stopTimes {
				stopTimesList = append(stopTimesList, models.RouteStopTime{
					ArrivalEnabled:   true,
					ArrivalTime:      models.NewModelDuration(time.Duration(st.ArrivalTime)),
					DepartureEnabled: true,
					DepartureTime:    models.NewModelDuration(time.Duration(st.DepartureTime)),
					ServiceID:        utils.FormCombinedID(agencyID, trip.ServiceID),
					StopHeadsign:     st.StopHeadsign.String,
					StopID:           utils.FormCombinedID(agencyID, st.StopID),
					TripID:           combinedTripID,
				})
			}
			tripsWithStopTimes = append(tripsWithStopTimes, models.TripStopTimes{
				TripID:    combinedTripID,
				StopTimes: stopTimesList,
			})
			stopTimesRefs = append(stopTimesRefs, stopTimesList)
		}

		formattedStopIDs := make([]string, len(orderedStopIDs))
		for i, sid := range orderedStopIDs {
			formattedStopIDs[i] = utils.FormCombinedID(agencyID, sid)
		}

		stopTripGroupings = append(stopTripGroupings, models.StopTripGrouping{
			DirectionID:        group.GroupID,
			TripHeadsigns:      headsigns,
			StopIDs:            formattedStopIDs,
			TripIDs:            tripIDs,
			TripsWithStopTimes: tripsWithStopTimes,
		})
	}

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
	references.Agencies = append(references.Agencies, agencyModel)

	references.Routes = utils.MapValues(routeRefs)

	tripIDs := make([]string, 0, len(tripIDsSet))
	for tid := range tripIDsSet {
		tripIDs = append(tripIDs, tid)
	}

	if len(tripIDs) > 0 {
		tripRows, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		for _, t := range tripRows {
			combinedTripID := utils.FormCombinedID(agencyID, t.ID)
			tripRef := models.NewTripReference(
				combinedTripID,
				t.RouteID,
				t.ServiceID,
				t.TripHeadsign.String,
				t.TripShortName.String,
				strconv.FormatInt(t.DirectionID.Int64, 10),
				utils.FormCombinedID(agencyID, t.BlockID.String),
				utils.FormCombinedID(agencyID, t.ShapeID.String),
			)
			references.Trips = append(references.Trips, *tripRef)
		}
	}

	uniqueStopIDs := make([]string, 0, len(globalStopIDSet))
	for sid := range globalStopIDSet {
		uniqueStopIDs = append(uniqueStopIDs, sid)
	}

	if len(uniqueStopIDs) > 0 {
		modelStops, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, uniqueStopIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Stops = append(references.Stops, modelStops...)
	}

	for _, sref := range stopTimesRefs {
		references.StopTimes = append(references.StopTimes, sref...)
	}

	entry := models.ScheduleForRouteEntry{
		RouteID:           utils.FormCombinedID(agencyID, routeID),
		ScheduleDate:      scheduleDate,
		ServiceIDs:        combinedServiceIDs,
		StopTripGroupings: stopTripGroupings,
	}
	api.sendResponse(w, r, models.NewEntryResponse(entry, *references, api.Clock))
}
