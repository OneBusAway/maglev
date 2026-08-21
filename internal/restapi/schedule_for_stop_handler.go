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

	// An unparseable date is a field error even when the ID resolves to nothing, so it has
	// to be caught before the agency lookup below. Resolving the date to a service date
	// needs that agency's timezone, so the parse itself stays where it is.
	if dateParam != "" {
		if err := utils.ValidateServiceDate(dateParam); err != nil {
			api.validationErrorResponse(w, r, map[string][]string{"date": {err.Error()}})
			return
		}
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

	var startOfDay time.Time
	var responseDate int64 // Stores the exact timestamp for the JSON response

	if dateParam != "" {
		var err error
		// The format was validated above, so this only fails on an unusable agency timezone.
		startOfDay, err = utils.ParseDate(dateParam, loc)
		if err != nil {
			api.serverErrorResponse(w, r, err)
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

	// A stop with no routes falls through the normal path: GetScheduleForStopOnDate
	// expands an empty route ID slice to IN (NULL) and returns no rows.
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

	frequenciesByTrip, err := api.fetchFrequenciesForScheduleRows(ctx, scheduleRows)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Group schedule data by route -> direction, per spec steps 6-7.
	routeDirectionScheduleMap, err := groupScheduleRowsByRouteAndDirection(
		ctx, scheduleRows, scheduleRowContext{
			agencyID:                   agencyID,
			startOfDay:                 startOfDay,
			activeServiceBlockTripsMap: activeServiceBlockTripsMap,
			frequenciesByTrip:          frequenciesByTrip,
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

		for _, group := range directionMap {
			directionSchedule := models.NewStopRouteDirectionSchedule(
				group.representativeHeadsign(), group.stopTimes, group.frequencies)
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
		references, err = api.buildScheduleForStopReferences(ctx, agencyID, stop, routesForStop, routeIDs)
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
// References describe the routes serving the stop, not just the ones running on
// the queried date, so they stay populated on dates with no active service.
func (api *RestAPI) buildScheduleForStopReferences(
	ctx context.Context,
	agencyID string,
	stop gtfsdb.Stop,
	routesForStop []gtfsdb.GetRoutesForStopRow,
	routeIDs []string,
) (*models.ReferencesModel, error) {
	routeRefs, agencyIDs := buildRouteRefs(agencyID, routesForStop)

	agencyRefs, err := api.fetchAgencyRefs(ctx, agencyIDs)
	if err != nil {
		return nil, err
	}

	references := models.NewEmptyReferences()
	references.Routes = utils.MapValues(routeRefs)
	references.Agencies = utils.MapValues(agencyRefs)
	references.Stops = append(references.Stops, buildQueriedStopRef(agencyID, stop, routeIDs))

	return references, nil
}

// buildRouteRefs converts the stop's routes into a combined-ID-keyed reference map,
// alongside the distinct agency IDs those routes belong to.
func buildRouteRefs(agencyID string, routesForStop []gtfsdb.GetRoutesForStopRow) (map[string]models.Route, []string) {
	routeRefs := make(map[string]models.Route, len(routesForStop))
	agencyIDs := make([]string, 0, len(routesForStop))
	seenAgencies := make(map[string]bool, len(routesForStop))

	for _, route := range routesForStop {
		combinedRouteID := utils.FormCombinedID(agencyID, route.ID)
		routeRefs[combinedRouteID] = models.NewRoute(
			combinedRouteID,
			route.AgencyID,
			nulls.StringOrEmpty(route.ShortName),
			nulls.StringOrEmpty(route.LongName),
			nulls.StringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			nulls.StringOrEmpty(route.Url),
			nulls.StringOrEmpty(route.Color),
			nulls.StringOrEmpty(route.TextColor))

		if !seenAgencies[route.AgencyID] {
			seenAgencies[route.AgencyID] = true
			agencyIDs = append(agencyIDs, route.AgencyID)
		}
	}

	return routeRefs, agencyIDs
}

// fetchAgencyRefs batch-fetches agencies by ID and builds their
// ID-keyed reference map.
func (api *RestAPI) fetchAgencyRefs(ctx context.Context, agencyIDs []string) (map[string]models.AgencyReference, error) {
	agencyRefs := make(map[string]models.AgencyReference, len(agencyIDs))
	if len(agencyIDs) == 0 {
		return agencyRefs, nil
	}

	fetchedAgencies, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(ctx, agencyIDs)
	if err != nil {
		return nil, err
	}

	for _, a := range fetchedAgencies {
		agencyRefs[a.ID] = models.AgencyReferenceFromDatabase(&a)
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
	// frequenciesByTrip maps trip ID to that trip's frequency windows. A trip listed here
	// runs on a headway rather than a fixed timetable, so its stop times are reported as
	// scheduleFrequencies instead of scheduleStopTimes.
	frequenciesByTrip map[string][]gtfsdb.Frequency
}

// directionScheduleGroup accumulates one direction's service at the stop: fixed-schedule
// stop times, headway-based frequency windows, and the headsign votes that decide the
// group's representative tripHeadsign.
type directionScheduleGroup struct {
	stopTimes     []models.ScheduleStopTime
	frequencies   []models.ScheduleFrequency
	headsignVotes map[string]int
}

// recordHeadsignVote adds weight votes for headsign. A fixed-schedule stop time casts one
// vote; a frequency window casts one per run it is expected to make, per spec step 7.
// Blank or absent headsigns cast no vote.
func (g *directionScheduleGroup) recordHeadsignVote(headsign sql.NullString, weight int) {
	if !headsign.Valid || headsign.String == "" || weight <= 0 {
		return
	}
	g.headsignVotes[headsign.String] += weight
}

// representativeHeadsign returns the group's plurality headsign, breaking ties in favour
// of the alphabetically first one so the response stays stable across requests.
func (g *directionScheduleGroup) representativeHeadsign() string {
	headsigns := make([]string, 0, len(g.headsignVotes))
	for headsign := range g.headsignVotes {
		headsigns = append(headsigns, headsign)
	}
	slices.Sort(headsigns)

	representative := ""
	maxVotes := 0
	for _, headsign := range headsigns {
		if votes := g.headsignVotes[headsign]; votes > maxVotes {
			maxVotes = votes
			representative = headsign
		}
	}

	return representative
}

// groupScheduleRowsByRouteAndDirection partitions schedule rows first by route, then by
// GTFS direction_id (defaulting to "0" when absent), per spec steps 6-7. Returns a
// non-nil error only if ctx is canceled mid-computation.
func groupScheduleRowsByRouteAndDirection(
	ctx context.Context,
	scheduleRows []gtfsdb.GetScheduleForStopOnDateRow,
	rowCtx scheduleRowContext,
) (map[string]map[string]*directionScheduleGroup, error) {
	routeDirectionScheduleMap := make(map[string]map[string]*directionScheduleGroup)

	for _, row := range scheduleRows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		combinedRouteID := utils.FormCombinedID(rowCtx.agencyID, row.RouteID)
		group := directionGroupFor(routeDirectionScheduleMap, combinedRouteID, directionIDForRow(row))

		frequencies := rowCtx.frequenciesByTrip[row.TripID]
		if len(frequencies) == 0 {
			group.stopTimes = append(group.stopTimes, buildScheduleStopTime(row, rowCtx))
			group.recordHeadsignVote(row.TripHeadsign, 1)
			continue
		}

		group.frequencies = append(group.frequencies, buildScheduleFrequencies(row, frequencies, rowCtx)...)
		group.recordHeadsignVote(row.TripHeadsign, estimatedRunCount(frequencies))
	}

	sortFrequenciesByStartTime(routeDirectionScheduleMap)

	return routeDirectionScheduleMap, nil
}

// directionGroupFor returns the accumulator for one route's direction, creating the
// intermediate map and group on first use.
func directionGroupFor(
	routeDirectionScheduleMap map[string]map[string]*directionScheduleGroup,
	combinedRouteID, directionID string,
) *directionScheduleGroup {
	if routeDirectionScheduleMap[combinedRouteID] == nil {
		routeDirectionScheduleMap[combinedRouteID] = make(map[string]*directionScheduleGroup)
	}
	if routeDirectionScheduleMap[combinedRouteID][directionID] == nil {
		routeDirectionScheduleMap[combinedRouteID][directionID] = &directionScheduleGroup{
			headsignVotes: make(map[string]int),
		}
	}

	return routeDirectionScheduleMap[combinedRouteID][directionID]
}

// sortFrequenciesByStartTime orders every direction group's frequency windows by start
// time, per spec step 8. Fixed-schedule stop times arrive already ordered by departure
// time from the query.
func sortFrequenciesByStartTime(routeDirectionScheduleMap map[string]map[string]*directionScheduleGroup) {
	for _, directionMap := range routeDirectionScheduleMap {
		for _, group := range directionMap {
			slices.SortStableFunc(group.frequencies, func(a, b models.ScheduleFrequency) int {
				return a.StartTime.Time.Compare(b.StartTime.Time)
			})
		}
	}
}

// estimatedRunCount is how many times a trip is expected to serve the stop across its
// frequency windows, used to weight its headsign vote against fixed-schedule trips.
func estimatedRunCount(frequencies []gtfsdb.Frequency) int {
	runs := 0
	for _, frequency := range frequencies {
		headway := time.Duration(frequency.HeadwaySecs) * time.Second
		window := time.Duration(frequency.EndTime - frequency.StartTime)
		runs += int(window / headway)
	}

	return runs
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

// fetchFrequenciesForScheduleRows batch-fetches the frequency windows of every trip in the
// schedule, keyed by trip ID. Trips absent from the result run on a fixed timetable.
func (api *RestAPI) fetchFrequenciesForScheduleRows(
	ctx context.Context,
	scheduleRows []gtfsdb.GetScheduleForStopOnDateRow,
) (map[string][]gtfsdb.Frequency, error) {
	tripIDs := make([]string, 0, len(scheduleRows))
	seenTrips := make(map[string]bool, len(scheduleRows))
	for _, row := range scheduleRows {
		if !seenTrips[row.TripID] {
			seenTrips[row.TripID] = true
			tripIDs = append(tripIDs, row.TripID)
		}
	}

	if len(tripIDs) == 0 {
		return nil, nil
	}

	frequencyRows, err := api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrips(ctx, tripIDs)
	if err != nil {
		return nil, err
	}

	frequenciesByTrip := make(map[string][]gtfsdb.Frequency)
	for _, frequency := range frequencyRows {
		frequenciesByTrip[frequency.TripID] = append(frequenciesByTrip[frequency.TripID], frequency)
	}

	return frequenciesByTrip, nil
}

// buildScheduleFrequencies converts a headway-based trip's frequency windows into
// scheduleFrequencies entries for the stop the row belongs to. A trip may declare several
// windows in a day (a morning headway and an evening one, say), each becoming its own entry.
func buildScheduleFrequencies(
	row gtfsdb.GetScheduleForStopOnDateRow,
	frequencies []gtfsdb.Frequency,
	rowCtx scheduleRowContext,
) []models.ScheduleFrequency {
	isFirstInBlock, isLastInBlock := blockBoundaries(row, rowCtx.activeServiceBlockTripsMap)

	scheduleFrequencies := make([]models.ScheduleFrequency, 0, len(frequencies))
	for _, frequency := range frequencies {
		scheduleFrequencies = append(scheduleFrequencies, models.ScheduleFrequency{
			FrequencyWindow:  models.NewFrequencyWindowFromDB(frequency, rowCtx.startOfDay),
			ServiceDate:      models.NewModelTime(rowCtx.startOfDay),
			ServiceID:        utils.FormCombinedID(rowCtx.agencyID, row.ServiceID),
			TripID:           utils.FormCombinedID(rowCtx.agencyID, row.TripID),
			StopHeadsign:     nulls.StringOrEmpty(row.StopHeadsign),
			ArrivalEnabled:   !isFirstInBlock,
			DepartureEnabled: !isLastInBlock,
		})
	}

	return scheduleFrequencies
}
