package restapi

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// scheduleForStopHandler returns the full schedule for a stop on a given date,
// including arrival and departure times grouped by route.
func (api *RestAPI) scheduleForStopHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, stopID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	// Get the date parameter or use current date
	dateParam := r.URL.Query().Get("date")

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

	var startOfDay time.Time
	var responseDate int64 // Stores the exact timestamp for the JSON response

	if dateParam != "" {
		var err error
		startOfDay, err = utils.ParseDate(dateParam, loc)
		if err != nil {
			fieldErrors := map[string][]string{
				"date": {err.Error()},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}

		// Echo the exact Unix timestamp if provided, else use midnight
		if unixMillis, err := strconv.ParseInt(dateParam, 10, 64); err == nil {
			responseDate = unixMillis
		} else {
			responseDate = startOfDay.UnixMilli()
		}
	} else {
		now := api.Clock.Now().In(loc)
		// Echo current wall-clock time if omitted
		responseDate = now.UnixMilli()

		y, m, d := now.Date()
		startOfDay = time.Date(y, m, d, 0, 0, 0, 0, loc)
	}

	targetDate := startOfDay.Format("20060102")
	weekday := strings.ToLower(startOfDay.Weekday().String())

	// Verify stop exists
	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	routesForStop, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routeIDs := make([]string, 0, len(routesForStop))
	for _, rt := range routesForStop {
		routeIDs = append(routeIDs, rt.ID)
	}

	if len(routeIDs) == 0 {
		api.sendResponse(w, r, models.NewEntryResponse(
			models.NewScheduleForStopEntry(utils.FormCombinedID(agencyID, stopID), responseDate, nil),
			*models.NewEmptyReferences(),
			api.Clock,
		))
		return
	}

	params := gtfsdb.GetScheduleForStopOnDateParams{
		StopID:     stopID,
		TargetDate: targetDate,
		Weekday:    weekday,
		RouteIds:   routeIDs,
	}
	scheduleRows, err := api.GtfsManager.GtfsDB.Queries.GetScheduleForStopOnDate(ctx, params)

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Build references maps
	agencyRefs := make(map[string]models.AgencyReference)

	// add the already fetched agency
	agencyRefs[agencyID] = models.NewAgencyReference(
		agency.ID,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Lang.String,
		agency.Phone.String,
		agency.Email.String,
		agency.FareUrl.String,
		"",    // disclaimer
		false, // privateService
	)

	routeRefs := make(map[string]models.Route)

	// Pre-process to gather unique IDs for batch fetching
	uniqueRouteIDsMap := make(map[string]bool)
	uniqueAgencyIDsMap := make(map[string]bool)

	for _, row := range scheduleRows {
		uniqueRouteIDsMap[row.RouteID] = true
		uniqueAgencyIDsMap[row.AgencyID] = true
	}

	// Batch fetch routes
	routeIDsToFetch := make([]string, 0, len(uniqueRouteIDsMap))
	for routeID := range uniqueRouteIDsMap {
		routeIDsToFetch = append(routeIDsToFetch, routeID)
	}

	if len(routeIDsToFetch) > 0 {
		fetchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDsToFetch)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		for _, route := range fetchedRoutes {
			combinedRouteID := utils.FormCombinedID(agencyID, route.ID)
			routeRefs[combinedRouteID] = models.NewRoute(
				combinedRouteID,
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String)
		}
	}

	agencyIDsToFetch := make([]string, 0, len(uniqueAgencyIDsMap))
	for agencyID := range uniqueAgencyIDsMap {
		agencyIDsToFetch = append(agencyIDsToFetch, agencyID)
	}

	fetchedAgencies, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(ctx, agencyIDsToFetch)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	for _, a := range fetchedAgencies {
		if _, exists := agencyRefs[a.ID]; !exists {
			agencyRefs[a.ID] = models.NewAgencyReference(
				a.ID,
				a.Name,
				a.Url,
				a.Timezone,
				nulls.StringOrEmpty(a.Lang),
				a.Phone.String,
				a.Email.String,
				a.FareUrl.String,
				"",    // disclaimer
				false, // privateService
			)
		}
	}

	// Extract unique block IDs directly from the scheduled rows
	uniqueBlockIDsMap := make(map[string]bool)
	for _, row := range scheduleRows {
		if row.BlockID.Valid && row.BlockID.String != "" {
			uniqueBlockIDsMap[row.BlockID.String] = true
		}
	}

	// Batch fetch all trips within the identified blocks for the active service day
	// This allows us to establish the chronological sequence of trips per vehicle
	blockTripsMap := make(map[string][]gtfsdb.GetTripsByBlockIDsRow)
	uniqueBlockIDs := make([]sql.NullString, 0, len(uniqueBlockIDsMap))
	for blockID := range uniqueBlockIDsMap {
		uniqueBlockIDs = append(uniqueBlockIDs, nulls.String(blockID))
	}

	if len(uniqueBlockIDs) > 0 {
		activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, targetDate)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		if len(activeServiceIDs) > 0 {
			blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDs(ctx, gtfsdb.GetTripsByBlockIDsParams{
				BlockIds:   uniqueBlockIDs,
				ServiceIds: activeServiceIDs,
			})
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			// Group trips by block ID. The underlying query inherently sorts by min_arrival_time ASC.
			for _, bt := range blockTrips {
				blockTripsMap[bt.BlockID.String] = append(blockTripsMap[bt.BlockID.String], bt)
			}
		}
	}

	// Group schedule data by route
	routeScheduleMap := make(map[string][]models.ScheduleStopTime)
	// Track headsign counts to pick the most common one
	routeHeadsignCounts := make(map[string]map[string]int)

	for _, row := range scheduleRows {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		combinedRouteID := utils.FormCombinedID(agencyID, row.RouteID)
		combinedTripID := utils.FormCombinedID(agencyID, row.TripID)

		// Convert GTFS time (nanoseconds since midnight) to Unix timestamp in the agency's timezone in milliseconds
		// GTFS times are stored as time.Duration values (nanoseconds), need to add to the target date
		arrivalDuration := time.Duration(row.ArrivalTime)
		departureDuration := time.Duration(row.DepartureTime)
		arrivalTimeMs := startOfDay.Add(arrivalDuration).UnixMilli()
		departureTimeMs := startOfDay.Add(departureDuration).UnixMilli()

		stopTime := models.NewScheduleStopTime(
			arrivalTimeMs,
			departureTimeMs,
			utils.FormCombinedID(agencyID, row.ServiceID),
			row.StopHeadsign.String,
			combinedTripID,
		)

		// Determine the arrival/departure capabilities for this stop time based on its
		// position within the vehicle's entire block for the service day.

		// First, verify if the stop is at the temporal boundaries of its individual trip.
		isFirstInTrip := row.MinArrivalTime.Valid && row.ArrivalTime == row.MinArrivalTime.Int64
		isLastInTrip := row.MaxDepartureTime.Valid && row.DepartureTime == row.MaxDepartureTime.Int64

		isFirstInBlock := isFirstInTrip
		isLastInBlock := isLastInTrip

		// If the trip belongs to a block, refine the boundaries to the block level.
		if row.BlockID.Valid && row.BlockID.String != "" {
			if bTrips, exists := blockTripsMap[row.BlockID.String]; exists && len(bTrips) > 0 {
				isFirstInBlock = isFirstInTrip && (bTrips[0].ID == row.TripID)
				isLastInBlock = isLastInTrip && (bTrips[len(bTrips)-1].ID == row.TripID)
			}
		}

		// Disable arrivals for the first stop of a block (vehicle starts service here).
		if isFirstInBlock {
			stopTime.ArrivalEnabled = false
		}
		// Disable departures for the last stop of a block (vehicle ends service here).
		if isLastInBlock {
			stopTime.DepartureEnabled = false
		}

		routeScheduleMap[combinedRouteID] = append(routeScheduleMap[combinedRouteID], stopTime)

		if row.TripHeadsign.Valid && row.TripHeadsign.String != "" {
			if routeHeadsignCounts[combinedRouteID] == nil {
				routeHeadsignCounts[combinedRouteID] = make(map[string]int)
			}
			routeHeadsignCounts[combinedRouteID][row.TripHeadsign.String]++
		}
	}

	// Build the route schedules
	var routeSchedules []models.StopRouteSchedule
	for routeID, stopTimes := range routeScheduleMap {
		// Select the most common headsign for this route
		tripHeadsign := ""
		maxCount := 0
		if headsigns, exists := routeHeadsignCounts[routeID]; exists {
			for headsign, count := range headsigns {
				if count > maxCount {
					maxCount = count
					tripHeadsign = headsign
				}
			}
		}

		directionSchedule := models.NewStopRouteDirectionSchedule(tripHeadsign, stopTimes, nil)
		routeSchedule := models.NewStopRouteSchedule(routeID, []models.StopRouteDirectionSchedule{directionSchedule})
		routeSchedules = append(routeSchedules, routeSchedule)
	}

	// Create the entry
	combinedStopID := utils.FormCombinedID(agencyID, stopID)
	entry := models.NewScheduleForStopEntry(combinedStopID, responseDate, routeSchedules)

	// Convert reference maps to slices
	references := models.NewEmptyReferences()
	references.Agencies = utils.MapValues(agencyRefs)
	references.Routes = utils.MapValues(routeRefs)

	routeIDsWithAgency := make([]string, 0, len(routeIDs))
	for _, ri := range routeIDs {
		routeIDsWithAgency = append(routeIDsWithAgency, utils.FormCombinedID(agencyID, ri))
	}

	stopRef := models.NewStop(
		nulls.StringOrEmpty(stop.Code),
		nulls.StringOrEmpty(stop.Direction),
		utils.FormCombinedID(agencyID, stop.ID),
		nulls.StringOrEmpty(stop.Name),
		"",
		utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		stop.Lat,
		stop.Lon,
		int(stop.LocationType.Int64),
		routeIDsWithAgency,
		routeIDsWithAgency,
	)

	references.Stops = append(references.Stops, stopRef)
	// Create and send response
	response := models.NewEntryResponse(entry, *references, api.Clock)
	api.sendResponse(w, r, response)
}
