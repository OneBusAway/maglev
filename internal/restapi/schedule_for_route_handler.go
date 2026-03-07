package restapi

import (
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) scheduleForRouteHandler(w http.ResponseWriter, r *http.Request) {
	parsed, _ := utils.GetParsedIDFromContext(r.Context())
	agencyID := parsed.AgencyID
	routeID := parsed.CodeID

	dateParam := r.URL.Query().Get("date")
	if err := utils.ValidateDate(dateParam); err != nil {
		fieldErrors := map[string][]string{
			"date": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

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
	loc := utils.LoadLocationWithUTCFallBack(agency.Timezone, agency.ID)

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
			Stops:             []models.Stop{},
			Trips:             []models.Trip{},
		}
		api.sendResponse(w, r, models.NewEntryResponse(entry, models.NewEmptyReferences(), api.Clock))
		return
	}

	combinedServiceIDs := make([]string, 0, len(serviceIDs))
	for _, sid := range serviceIDs {
		combinedServiceIDs = append(combinedServiceIDs, utils.FormCombinedID(agencyID, sid))
	}

	trips, err := api.GtfsManager.GtfsDB.Queries.GetTripsForRouteInActiveServiceIDs(ctx, gtfsdb.GetTripsForRouteInActiveServiceIDsParams{
		RouteID:    routeID,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Handle case where service exists but this route has no trips today.
	// Return 200 OK with empty data.
	if len(trips) == 0 {
		entry := models.ScheduleForRouteEntry{
			RouteID:           utils.FormCombinedID(agencyID, routeID),
			ScheduleDate:      scheduleDate,
			ServiceIDs:        combinedServiceIDs,
			StopTripGroupings: []models.StopTripGrouping{},
			Stops:             []models.Stop{},
			Trips:             []models.Trip{},
		}
		api.sendResponse(w, r, models.NewEntryResponse(entry, models.NewEmptyReferences(), api.Clock))
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

	groupings := make(map[string][]gtfsdb.Trip)
	for _, trip := range trips {
		tripIDsSet[trip.ID] = true
		// The go-gtfs library encodes direction_id as a 3-value enum:
		//   0 = Unspecified, 1 = True (GTFS direction_id=1), 2 = False (GTFS direction_id=0)
		dirID := "0"
		if trip.DirectionID.Int64 == 1 {
			dirID = "1"
		}
		groupings[dirID] = append(groupings[dirID], trip)
	}
	var stopTripGroupings []models.StopTripGrouping
	globalStopIDSet := make(map[string]struct{})
	var stopTimesRefs [][]models.RouteStopTime
	for dirID, groupedTrips := range groupings {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		stopIDSet := make(map[string]struct{})
		headsignSet := make(map[string]struct{})
		tripIDs := make([]string, 0, len(groupedTrips))
		tripsWithStopTimes := make([]models.TripStopTimes, 0, len(groupedTrips))
		for _, trip := range groupedTrips {
			combinedTripID := utils.FormCombinedID(agencyID, trip.ID)
			tripIDs = append(tripIDs, combinedTripID)
			if trip.TripHeadsign.String != "" {
				headsignSet[trip.TripHeadsign.String] = struct{}{}
			}
			stopIDs, err := api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, trip.ID)
			if err != nil {
				api.Logger.Warn("failed to fetch stop IDs for trip", "trip_id", trip.ID, "error", err)
				continue
			}
			for _, stopID := range stopIDs {
				stopIDSet[stopID] = struct{}{}
				globalStopIDSet[stopID] = struct{}{}
			}
			stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
			if err != nil {
				api.Logger.Warn("failed to fetch stop times for trip", "trip_id", trip.ID, "error", err)
				continue
			}
			stopTimesList := make([]models.RouteStopTime, 0, len(stopTimes))
			for _, st := range stopTimes {
				arrivalSec := int(utils.NanosToSeconds(st.ArrivalTime))
				departureSec := int(utils.NanosToSeconds(st.DepartureTime))
				stopTimesList = append(stopTimesList, models.RouteStopTime{
					ArrivalEnabled:   true,
					ArrivalTime:      arrivalSec,
					DepartureEnabled: true,
					DepartureTime:    departureSec,
					ServiceID:        utils.FormCombinedID(agencyID, trip.ServiceID),
					StopHeadsign:     st.StopHeadsign.String,
					StopID:           utils.FormCombinedID(agencyID, st.StopID),
					TripID:           utils.FormCombinedID(agencyID, trip.ID),
				})
			}
			tripsWithStopTimes = append(tripsWithStopTimes, models.TripStopTimes{
				TripID:    utils.FormCombinedID(agencyID, trip.ID),
				StopTimes: stopTimesList,
			})
			stopTimesRefs = append(stopTimesRefs, stopTimesList)
		}
		stopIDsOrdered := make([]string, 0, len(stopIDSet))
		for stopID := range stopIDSet {
			stopIDsOrdered = append(stopIDsOrdered, utils.FormCombinedID(agencyID, stopID))
		}
		headsigns := make([]string, 0, len(headsignSet))
		for h := range headsignSet {
			headsigns = append(headsigns, h)
		}
		stopTripGroupings = append(stopTripGroupings, models.StopTripGrouping{
			DirectionID:        dirID,
			TripHeadsigns:      headsigns,
			StopIDs:            stopIDsOrdered,
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

	for _, r := range routeRefs {
		references.Routes = append(references.Routes, r)
	}

	tripIDs := make([]string, 0, len(tripIDsSet))
	for tid := range tripIDsSet {
		tripIDs = append(tripIDs, tid)
	}

	entryTrips := make([]models.Trip, 0)

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
			entryTrips = append(entryTrips, *tripRef)
		}
	}

	uniqueStopIDs := make([]string, 0, len(globalStopIDSet))
	for sid := range globalStopIDSet {
		uniqueStopIDs = append(uniqueStopIDs, sid)
	}

	entryStops := make([]models.Stop, 0)

	if len(uniqueStopIDs) > 0 {

		modelStops, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, uniqueStopIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Stops = append(references.Stops, modelStops...)
		entryStops = modelStops
	}

	for _, sref := range stopTimesRefs {
		references.StopTimes = append(references.StopTimes, sref...)
	}

	entry := models.ScheduleForRouteEntry{
		RouteID:           utils.FormCombinedID(agencyID, routeID),
		ScheduleDate:      scheduleDate,
		ServiceIDs:        combinedServiceIDs,
		StopTripGroupings: stopTripGroupings,
		Stops:             entryStops,
		Trips:             entryTrips,
	}
	api.sendResponse(w, r, models.NewEntryResponse(entry, references, api.Clock))
}
