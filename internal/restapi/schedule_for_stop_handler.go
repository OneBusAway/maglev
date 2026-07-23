package restapi

import (
	"cmp"
	"context"
	"database/sql"
	"net/http"
	"slices"
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

	// Natural-sort by short name (falling back to long name, then agency/route ID) so that
	// stopRouteSchedules can later be emitted in this same order, per spec.
	utils.SortRoutesForStopRowsByName(routesForStop)

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

	// Extract unique block IDs directly from the scheduled rows
	uniqueBlockIDsMap := make(map[string]bool)
	for _, row := range scheduleRows {
		if row.BlockID.Valid && row.BlockID.String != "" {
			uniqueBlockIDsMap[row.BlockID.String] = true
		}
	}

	// Batch fetch all trips within the identified blocks for the active service day
	// This allows us to establish the chronological sequence of trips per vehicle
	activeServiceBlockTripsMap := make(map[string][]gtfsdb.GetTripsByBlockIDsRow)
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
				activeServiceBlockTripsMap[bt.BlockID.String] = append(activeServiceBlockTripsMap[bt.BlockID.String], bt)
			}
		}
	}

	// Group schedule data by route -> direction -> slice of stop times, and track
	// per-direction headsign vote counts, per spec steps 6-7.
	routeDirectionScheduleMap, routeDirectionHeadsignCounts, err := groupScheduleRowsByRouteAndDirection(
		ctx, scheduleRows, scheduleRowContext{
			agencyID:                   agencyID,
			startOfDay:                 startOfDay,
			activeServiceBlockTripsMap: activeServiceBlockTripsMap,
		},
	)
	if err != nil {
		api.clientCanceledResponse(w, r, err)
		return
	}

	// Build the route schedules in the natural-sort order established above (spec step 10):
	// by route short name, falling back to long name, then agency/route ID.
	var routeSchedules []models.StopRouteSchedule
	for _, rt := range routesForStop {
		combinedRouteID := utils.FormCombinedID(agencyID, rt.ID)
		directionMap, hasSchedule := routeDirectionScheduleMap[combinedRouteID]
		if !hasSchedule {
			continue
		}

		var directionSchedules []models.StopRouteDirectionSchedule

		for dirID, stopTimes := range directionMap {
			tripHeadsign := ""
			maxCount := 0
			if dirHeadsigns, exists := routeDirectionHeadsignCounts[combinedRouteID][dirID]; exists {
				headsigns := make([]string, 0, len(dirHeadsigns))
				for headsign := range dirHeadsigns {
					headsigns = append(headsigns, headsign)
				}
				slices.Sort(headsigns)
				for _, headsign := range headsigns {
					count := dirHeadsigns[headsign]
					if count > maxCount {
						maxCount = count
						tripHeadsign = headsign
					}
				}
			}

			directionSchedule := models.NewStopRouteDirectionSchedule(tripHeadsign, stopTimes, nil)
			directionSchedules = append(directionSchedules, directionSchedule)
		}

		// Sort direction groups alphabetically by headsign
		slices.SortStableFunc(directionSchedules, func(a, b models.StopRouteDirectionSchedule) int {
			return cmp.Compare(a.TripHeadsign, b.TripHeadsign)
		})

		routeSchedule := models.NewStopRouteSchedule(combinedRouteID, directionSchedules)
		routeSchedules = append(routeSchedules, routeSchedule)
	}

	// Create the entry
	combinedStopID := utils.FormCombinedID(agencyID, stopID)
	entry := models.NewScheduleForStopEntry(combinedStopID, responseDate, routeSchedules)

	references := models.NewEmptyReferences()
	if ShouldIncludeReferences(r) {
		references, err = api.buildScheduleForStopReferences(ctx, agencyID, agency, stop, scheduleRows, routeIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	// Create and send response
	response := models.NewEntryResponse(entry, *references, api.Clock)
	api.sendResponse(w, r, response)
}

// buildScheduleForStopReferences builds the agency, route, and stop references
// for the schedule-for-stop entry. Only called when includeReferences=true.
func (api *RestAPI) buildScheduleForStopReferences(
	ctx context.Context,
	agencyID string,
	agency gtfsdb.Agency,
	stop gtfsdb.Stop,
	scheduleRows []gtfsdb.GetScheduleForStopOnDateRow,
	routeIDs []string,
) (*models.ReferencesModel, error) {
	routeIDsToFetch, agencyIDsToFetch := collectRouteAndAgencyIDs(scheduleRows)

	routeRefs, err := api.fetchRouteRefs(ctx, agencyID, routeIDsToFetch)
	if err != nil {
		return nil, err
	}

	agencyRefs, err := api.fetchAgencyRefs(ctx, agency, agencyIDsToFetch)
	if err != nil {
		return nil, err
	}

	references := models.NewEmptyReferences()
	references.Routes = utils.MapValues(routeRefs)
	references.Agencies = utils.MapValues(agencyRefs)
	references.Stops = append(references.Stops, buildQueriedStopRef(agencyID, stop, routeIDs))

	return references, nil
}

// collectRouteAndAgencyIDs collects the distinct route and agency IDs referenced
// across a stop's schedule rows, for batch fetching.
func collectRouteAndAgencyIDs(scheduleRows []gtfsdb.GetScheduleForStopOnDateRow) (routeIDs, agencyIDs []string) {
	uniqueRouteIDs := make(map[string]bool)
	uniqueAgencyIDs := make(map[string]bool)

	for _, row := range scheduleRows {
		uniqueRouteIDs[row.RouteID] = true
		uniqueAgencyIDs[row.AgencyID] = true
	}

	routeIDs = make([]string, 0, len(uniqueRouteIDs))
	for id := range uniqueRouteIDs {
		routeIDs = append(routeIDs, id)
	}

	agencyIDs = make([]string, 0, len(uniqueAgencyIDs))
	for id := range uniqueAgencyIDs {
		agencyIDs = append(agencyIDs, id)
	}

	return routeIDs, agencyIDs
}

// fetchRouteRefs batch-fetches routes by ID and builds their
// combined-ID-keyed reference map.
func (api *RestAPI) fetchRouteRefs(ctx context.Context, agencyID string, routeIDs []string) (map[string]models.Route, error) {
	routeRefs := make(map[string]models.Route)
	if len(routeIDs) == 0 {
		return routeRefs, nil
	}

	fetchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
	if err != nil {
		return nil, err
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

	return routeRefs, nil
}

// fetchAgencyRefs batch-fetches agencies by ID and builds their
// ID-keyed reference map, seeded with the stop's own already-fetched agency.
func (api *RestAPI) fetchAgencyRefs(ctx context.Context, seedAgency gtfsdb.Agency, agencyIDs []string) (map[string]models.AgencyReference, error) {
	agencyRefs := make(map[string]models.AgencyReference)
	agencyRefs[seedAgency.ID] = models.AgencyReferenceFromDatabase(&seedAgency)

	fetchedAgencies, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(ctx, agencyIDs)
	if err != nil {
		return nil, err
	}

	for _, a := range fetchedAgencies {
		if _, exists := agencyRefs[a.ID]; !exists {
			agencyRefs[a.ID] = models.AgencyReferenceFromDatabase(&a)
		}
	}

	return agencyRefs, nil
}

// buildQueriedStopRef builds the full stop reference for the queried stop.
func buildQueriedStopRef(agencyID string, stop gtfsdb.Stop, routeIDs []string) models.Stop {
	routeIDsWithAgency := make([]string, 0, len(routeIDs))
	for _, ri := range routeIDs {
		routeIDsWithAgency = append(routeIDsWithAgency, utils.FormCombinedID(agencyID, ri))
	}

	return models.NewStop(
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
}

// scheduleRowContext holds the values that stay constant across every row while building
// a stop's schedule, so callers don't have to thread each one individually through the
// row-building helpers below.
type scheduleRowContext struct {
	agencyID   string
	startOfDay time.Time
	// activeServiceBlockTripsMap maps block ID to that block's trips, already filtered to
	// the queried date's active service IDs (see GetActiveServiceIDsForDate). The name is
	// load-bearing: blockBoundaries's first/last-in-block comparisons are only correct
	// against a map pre-filtered this way.
	activeServiceBlockTripsMap map[string][]gtfsdb.GetTripsByBlockIDsRow
}

// groupScheduleRowsByRouteAndDirection partitions schedule rows first by route, then by
// GTFS direction_id (defaulting to "0" when absent), per spec steps 6-7. It returns the
// grouped stop times alongside per-direction headsign vote counts used to pick each
// direction group's representative tripHeadsign. Returns a non-nil error only if ctx is
// canceled mid-computation.
func groupScheduleRowsByRouteAndDirection(
	ctx context.Context,
	scheduleRows []gtfsdb.GetScheduleForStopOnDateRow,
	rowCtx scheduleRowContext,
) (
	routeDirectionScheduleMap map[string]map[string][]models.ScheduleStopTime,
	routeDirectionHeadsignCounts map[string]map[string]map[string]int,
	err error,
) {
	routeDirectionScheduleMap = make(map[string]map[string][]models.ScheduleStopTime)
	routeDirectionHeadsignCounts = make(map[string]map[string]map[string]int)

	for _, row := range scheduleRows {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}

		directionID := directionIDForRow(row)
		combinedRouteID := utils.FormCombinedID(rowCtx.agencyID, row.RouteID)
		stopTime := buildScheduleStopTime(row, rowCtx)

		addStopTimeToDirectionGroup(routeDirectionScheduleMap, combinedRouteID, directionID, stopTime)
		recordHeadsignVote(routeDirectionHeadsignCounts, combinedRouteID, directionID, row.TripHeadsign)
	}

	return routeDirectionScheduleMap, routeDirectionHeadsignCounts, nil
}

// directionIDForRow returns the row's GTFS direction_id as a string, defaulting to "0"
// when the feed does not specify one.
func directionIDForRow(row gtfsdb.GetScheduleForStopOnDateRow) string {
	if row.DirectionID.Valid {
		return strconv.FormatInt(row.DirectionID.Int64, 10)
	}
	return "0"
}

// buildScheduleStopTime converts a schedule row into a ScheduleStopTime, converting GTFS
// times (nanoseconds since midnight) to Unix millisecond timestamps and disabling the
// arrival/departure flags at the boundaries of the vehicle's block for the service day.
func buildScheduleStopTime(row gtfsdb.GetScheduleForStopOnDateRow, rowCtx scheduleRowContext) models.ScheduleStopTime {
	arrivalTimeMs := rowCtx.startOfDay.Add(time.Duration(row.ArrivalTime)).UnixMilli()
	departureTimeMs := rowCtx.startOfDay.Add(time.Duration(row.DepartureTime)).UnixMilli()

	stopTime := models.NewScheduleStopTime(
		arrivalTimeMs,
		departureTimeMs,
		utils.FormCombinedID(rowCtx.agencyID, row.ServiceID),
		row.StopHeadsign.String,
		utils.FormCombinedID(rowCtx.agencyID, row.TripID),
	)

	isFirstInBlock, isLastInBlock := blockBoundaries(row, rowCtx.activeServiceBlockTripsMap)
	// Disable arrivals for the first stop of a block (vehicle starts service here).
	if isFirstInBlock {
		stopTime.ArrivalEnabled = false
	}
	// Disable departures for the last stop of a block (vehicle ends service here).
	if isLastInBlock {
		stopTime.DepartureEnabled = false
	}

	return stopTime
}

// blockBoundaries reports whether this stop time is the first (or last) stop time in the
// vehicle's entire block for the service day, meaning there is no inbound arrival (or
// onward departure) to speak of. activeServiceBlockTripsMap must already be filtered to
// the queried date's active service IDs; passing an unfiltered map will silently produce
// wrong results.
func blockBoundaries(
	row gtfsdb.GetScheduleForStopOnDateRow,
	activeServiceBlockTripsMap map[string][]gtfsdb.GetTripsByBlockIDsRow,
) (isFirstInBlock, isLastInBlock bool) {
	// First, verify if the stop is at the temporal boundaries of its individual trip.
	isFirstInTrip := row.MinArrivalTime.Valid && row.ArrivalTime == row.MinArrivalTime.Int64
	isLastInTrip := row.MaxDepartureTime.Valid && row.DepartureTime == row.MaxDepartureTime.Int64

	if !row.BlockID.Valid || row.BlockID.String == "" {
		return isFirstInTrip, isLastInTrip
	}

	// If the trip belongs to a block, refine the boundaries to the block level.
	bTrips, exists := activeServiceBlockTripsMap[row.BlockID.String]
	if !exists || len(bTrips) == 0 {
		return isFirstInTrip, isLastInTrip
	}

	isFirstInBlock = isFirstInTrip && bTrips[0].ID == row.TripID
	isLastInBlock = isLastInTrip && bTrips[len(bTrips)-1].ID == row.TripID
	return isFirstInBlock, isLastInBlock
}

// addStopTimeToDirectionGroup appends stopTime to the route's direction bucket, creating
// the intermediate map when this is the route's first stop time seen so far.
func addStopTimeToDirectionGroup(
	routeDirectionScheduleMap map[string]map[string][]models.ScheduleStopTime,
	combinedRouteID, directionID string,
	stopTime models.ScheduleStopTime,
) {
	if routeDirectionScheduleMap[combinedRouteID] == nil {
		routeDirectionScheduleMap[combinedRouteID] = make(map[string][]models.ScheduleStopTime)
	}
	routeDirectionScheduleMap[combinedRouteID][directionID] = append(routeDirectionScheduleMap[combinedRouteID][directionID], stopTime)
}

// recordHeadsignVote tallies one vote for headsign under the route's direction bucket, used
// later to pick each direction group's plurality tripHeadsign. Blank/absent headsigns cast
// no vote.
func recordHeadsignVote(
	routeDirectionHeadsignCounts map[string]map[string]map[string]int,
	combinedRouteID, directionID string,
	headsign sql.NullString,
) {
	if !headsign.Valid || headsign.String == "" {
		return
	}

	if routeDirectionHeadsignCounts[combinedRouteID] == nil {
		routeDirectionHeadsignCounts[combinedRouteID] = make(map[string]map[string]int)
	}
	if routeDirectionHeadsignCounts[combinedRouteID][directionID] == nil {
		routeDirectionHeadsignCounts[combinedRouteID][directionID] = make(map[string]int)
	}
	routeDirectionHeadsignCounts[combinedRouteID][directionID][headsign.String]++
}
