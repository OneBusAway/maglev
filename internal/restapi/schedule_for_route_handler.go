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

	serviceIDs, err := api.GtfsManager.GetActiveServiceIDsForDateCached(ctx, targetDate)
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

		rawTripIDs := make([]string, 0, len(groupedTrips))
		for _, trip := range groupedTrips {
			rawTripIDs = append(rawTripIDs, trip.ID)
			if trip.TripHeadsign.String != "" {
				headsignSet[trip.TripHeadsign.String] = struct{}{}
			}
		}

		allStopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, rawTripIDs)
		if err != nil {
			api.Logger.Warn("failed to fetch stop times for trips in direction group", "dir_id", dirID, "error", err)
		}

		// Group stop times by trip ID (query returns rows ordered by trip_id, stop_sequence).
		stopTimesByTrip := make(map[string][]gtfsdb.StopTime, len(groupedTrips))
		for _, st := range allStopTimes {
			stopTimesByTrip[st.TripID] = append(stopTimesByTrip[st.TripID], st)
		}

		for _, trip := range groupedTrips {
			stopTimes := stopTimesByTrip[trip.ID]
			if len(stopTimes) == 0 {
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
				stopIDSet[st.StopID] = struct{}{}
				globalStopIDSet[st.StopID] = struct{}{}
			}
			tripIDs = append(tripIDs, utils.FormCombinedID(agencyID, trip.ID))
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
