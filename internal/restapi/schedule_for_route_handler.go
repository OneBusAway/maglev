package restapi

import (
	"net/http"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) scheduleForRouteHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)
	if err := utils.ValidateID(queryParamID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	agencyID, routeID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
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
	var targetDate string
	if dateParam != "" {
		parsedDate, err := time.Parse("2006-01-02", dateParam)
		if err != nil {
			fieldErrors := map[string][]string{
				"date": {"Invalid date format. Use YYYY-MM-DD"},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}
		targetDate = parsedDate.Format("20060102")
	} else {
		now := api.Clock.Now()
		targetDate = now.Format("20060102")
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
			ScheduleDate:      targetDate,
			ServiceIDs:        []string{},
			StopTripGroupings: []models.StopTripGrouping{},
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
			ScheduleDate:      targetDate,
			ServiceIDs:        combinedServiceIDs,
			StopTripGroupings: []models.StopTripGrouping{},
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
		route.TextColor.String,
		route.ShortName.String,
	)
	routeRefs[utils.FormCombinedID(agencyID, route.ID)] = routeModel

	type tripGroupKey struct {
		directionID  int64
		tripHeadsign string
	}
	groupings := make(map[tripGroupKey][]gtfsdb.Trip)
	for _, trip := range trips {
		tripIDsSet[trip.ID] = true
		key := tripGroupKey{directionID: trip.DirectionID.Int64 - 1, tripHeadsign: trip.TripHeadsign.String}
		groupings[key] = append(groupings[key], trip)
	}
	var stopTripGroupings []models.StopTripGrouping
	globalStopIDSet := make(map[string]struct{})
	var stopTimesRefs []interface{}
	for key, groupedTrips := range groupings {
		if ctx.Err() != nil {
			return
		}

		stopIDSet := make(map[string]struct{})
		tripIDs := make([]string, 0, len(groupedTrips))
		tripsWithStopTimes := make([]models.TripStopTimes, 0, len(groupedTrips))
		for _, trip := range groupedTrips {
			combinedTripID := utils.FormCombinedID(agencyID, trip.ID)
			tripIDs = append(tripIDs, combinedTripID)
			stopIDs, err := api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, trip.ID)
			if err != nil {
				continue
			}
			for _, stopID := range stopIDs {
				stopIDSet[stopID] = struct{}{}
				globalStopIDSet[stopID] = struct{}{}
			}
			stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
			if err != nil {
				continue
			}
			stopTimesList := make([]models.RouteStopTime, 0, len(stopTimes))
			for _, st := range stopTimes {
				arrivalSec := int(st.ArrivalTime / 1e9)
				departureSec := int(st.DepartureTime / 1e9)
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
		stopTripGroupings = append(stopTripGroupings, models.StopTripGrouping{
			DirectionID:        key.directionID,
			TripHeadsigns:      []string{key.tripHeadsign},
			StopIDs:            stopIDsOrdered,
			TripIDs:            tripIDs,
			TripsWithStopTimes: tripsWithStopTimes,
		})
	}

	references := models.NewEmptyReferences()
	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err == nil {
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
	}

	for _, r := range routeRefs {
		references.Routes = append(references.Routes, r)
	}

	tripIDs := make([]string, 0, len(tripIDsSet))
	for tid := range tripIDsSet {
		tripIDs = append(tripIDs, tid)
	}
	if len(tripIDs) > 0 {
		tripRows, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDs)
		if err == nil {
			for _, t := range tripRows {
				combinedTripID := utils.FormCombinedID(agencyID, t.ID)
				tripRef := models.NewTripReference(
					combinedTripID,
					t.RouteID,
					t.ServiceID,
					t.TripHeadsign.String,
					t.TripShortName.String,
					t.DirectionID.Int64,
					utils.FormCombinedID(agencyID, t.BlockID.String),
					utils.FormCombinedID(agencyID, t.ShapeID.String),
				)
				references.Trips = append(references.Trips, tripRef)
			}
		}
	}

	// Create a local calculator to ensure thread safety
	calc := gtfs.NewAdvancedDirectionCalculator(api.GtfsManager.GtfsDB.Queries)

	uniqueStopIDs := make([]string, 0, len(globalStopIDSet))
	for sid := range globalStopIDSet {
		uniqueStopIDs = append(uniqueStopIDs, sid)
	}
	if len(uniqueStopIDs) > 0 {
		// Pass the local calculator
		modelStops, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, uniqueStopIDs, calc)
		if err == nil {
			references.Stops = append(references.Stops, modelStops...)
		}
	}

	for _, sref := range stopTimesRefs {
		switch v := sref.(type) {
		case []models.RouteStopTime:
			for _, st := range v {
				references.StopTimes = append(references.StopTimes, st)
			}
		case []map[string]interface{}:
			for _, st := range v {
				references.StopTimes = append(references.StopTimes, st)
			}
		case []interface{}:
			references.StopTimes = append(references.StopTimes, v...)
		default:
			references.StopTimes = append(references.StopTimes, v)
		}
	}

	entry := models.ScheduleForRouteEntry{
		RouteID:           utils.FormCombinedID(agencyID, routeID),
		ScheduleDate:      targetDate,
		ServiceIDs:        combinedServiceIDs,
		StopTripGroupings: stopTripGroupings,
	}
	api.sendResponse(w, r, models.NewEntryResponse(entry, references, api.Clock))
}
