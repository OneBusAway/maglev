package restapi

import (
	"cmp"
	"context"
	"database/sql"
	"log/slog"
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

	// Batch-fetch frequencies for trips in this schedule (avoid N+1).
	freqMap := make(map[string][]gtfsdb.Frequency)
	if len(scheduleRows) > 0 {
		tripIDSet := make(map[string]bool, len(scheduleRows))
		for _, row := range scheduleRows {
			tripIDSet[row.TripID] = true
		}
		tripIDs := make([]string, 0, len(tripIDSet))
		for tripID := range tripIDSet {
			tripIDs = append(tripIDs, tripID)
		}
		var freqErr error
		freqMap, freqErr = api.fetchFrequenciesForTrips(ctx, tripIDs)
		if freqErr != nil {
			api.serverErrorResponse(w, r, freqErr)
			return
		}
	}

	// Group schedule data by route -> direction -> slice of stop times, and track
	// per-direction headsign vote counts, per spec steps 6-7.
	routeDirectionScheduleMap, routeDirectionFrequencyMap, routeDirectionHeadsignCounts, err := groupScheduleRowsByRouteAndDirection(
		ctx, scheduleRows, scheduleRowContext{
			agencyID:                   agencyID,
			startOfDay:                 startOfDay,
			activeServiceBlockTripsMap: activeServiceBlockTripsMap,
			freqMap:                    freqMap,
			logger:                     api.Logger,
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
		directionMap, hasStopTimes := routeDirectionScheduleMap[combinedRouteID]
		frequencyMap, hasFrequencies := routeDirectionFrequencyMap[combinedRouteID]
		if !hasStopTimes && !hasFrequencies {
			continue
		}

		// Iterate the union of direction IDs: frequency-only directions still need a group.
		dirIDs := make(map[string]bool, len(directionMap)+len(frequencyMap))
		for dirID := range directionMap {
			dirIDs[dirID] = true
		}
		for dirID := range frequencyMap {
			dirIDs[dirID] = true
		}

		var directionSchedules []models.StopRouteDirectionSchedule

		for dirID := range dirIDs {
			tripHeadsign := pluralityHeadsign(routeDirectionHeadsignCounts, combinedRouteID, dirID)

			frequencies := frequencyMap[dirID]
			// Java OBA orders scheduleFrequencies by start time (FrequencyBeanComparator).
			slices.SortStableFunc(frequencies, func(a, b models.ScheduleFrequency) int {
				return cmp.Compare(a.StartTime.UnixMilli(), b.StartTime.UnixMilli())
			})

			stopTimes := directionMap[dirID]
			if stopTimes == nil {
				stopTimes = []models.ScheduleStopTime{}
			}
			directionSchedule := models.NewStopRouteDirectionSchedule(tripHeadsign, stopTimes, frequencies)
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
	// freqMap maps trip ID to its frequency rows: exact_times=0 trips emit
	// scheduleFrequencies; exact_times=1 trips expand into stop times.
	freqMap map[string][]gtfsdb.Frequency
	// logger reports malformed GTFS data; nil in tests.
	logger *slog.Logger
}

// routeDirection keys per-route, per-direction output buckets.
type routeDirection struct {
	combinedRouteID string
	directionID     string
}

// routeDirectionAccumulators holds the per-route, per-direction output buckets
// built from schedule rows: stop times, frequencies, and headsign votes.
type routeDirectionAccumulators struct {
	stopTimes      map[string]map[string][]models.ScheduleStopTime
	frequencies    map[string]map[string][]models.ScheduleFrequency
	headsignCounts map[string]map[string]map[string]int
}

// groupScheduleRowsByRouteAndDirection partitions schedule rows by route, then direction
// (defaulting to "0"), per spec steps 6-7. exact_times=0 trips go to the frequency map;
// exact_times=1 trips expand into stop times.
// Returns a non-nil error only if ctx is canceled.
func groupScheduleRowsByRouteAndDirection(
	ctx context.Context,
	scheduleRows []gtfsdb.GetScheduleForStopOnDateRow,
	rowCtx scheduleRowContext,
) (
	routeDirectionScheduleMap map[string]map[string][]models.ScheduleStopTime,
	routeDirectionFrequencyMap map[string]map[string][]models.ScheduleFrequency,
	routeDirectionHeadsignCounts map[string]map[string]map[string]int,
	err error,
) {
	acc := &routeDirectionAccumulators{
		stopTimes:      make(map[string]map[string][]models.ScheduleStopTime),
		frequencies:    make(map[string]map[string][]models.ScheduleFrequency),
		headsignCounts: make(map[string]map[string]map[string]int),
	}

	for _, row := range scheduleRows {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}

		directionID := directionIDForRow(row)
		dir := routeDirection{
			combinedRouteID: utils.FormCombinedID(rowCtx.agencyID, row.RouteID),
			directionID:     directionID,
		}

		freqs, isFrequencyTrip := rowCtx.freqMap[row.TripID]
		if isFrequencyTrip && len(freqs) > 0 {
			processFrequencyTrip(row, freqs, rowCtx, acc, dir)
			continue
		}

		stopTime := buildScheduleStopTime(row, rowCtx)
		addStopTimeToDirectionGroup(acc, dir, stopTime)
		recordHeadsignVote(acc, dir, row.TripHeadsign, 1)
	}

	return acc.stopTimes, acc.frequencies, acc.headsignCounts, nil
}

// processFrequencyTrip routes a frequency trip's rows: exact_times=0 rows
// become schedule frequencies weighted by trips in the window, exact_times=1
// rows expand into discrete stop times (Java OBA).
func processFrequencyTrip(
	row gtfsdb.GetScheduleForStopOnDateRow,
	freqs []gtfsdb.Frequency,
	rowCtx scheduleRowContext,
	acc *routeDirectionAccumulators,
	dir routeDirection,
) {
	for _, freq := range freqs {
		if freq.ExactTimes != 0 {
			expanded := expandExactTimesStopTimes(row, freq, rowCtx)
			for _, st := range expanded {
				addStopTimeToDirectionGroup(acc, dir, st)
			}
			if len(expanded) > 0 {
				recordHeadsignVote(acc, dir, row.TripHeadsign, len(expanded))
			}
			continue
		}
		scheduleFreq := buildScheduleFrequency(row, freq, rowCtx)
		addFrequencyToDirectionGroup(acc, dir, scheduleFreq)
		// Weight frequency headsign votes by trips in the window (Java OBA).
		recordHeadsignVote(acc, dir, row.TripHeadsign, frequencyVoteWeight(freq))
	}
}

// directionIDForRow returns the row's GTFS direction_id as a string, defaulting to "0"
// when the feed does not specify one.
func directionIDForRow(row gtfsdb.GetScheduleForStopOnDateRow) string {
	if row.DirectionID.Valid {
		return strconv.FormatInt(row.DirectionID.Int64, 10)
	}
	return "0"
}

// newScheduleStopTime builds a ScheduleStopTime for the row, disabling the
// arrival/departure flags at the boundaries of its vehicle's block.
func newScheduleStopTime(row gtfsdb.GetScheduleForStopOnDateRow, rowCtx scheduleRowContext, arrivalMs, departureMs int64, isFirstInBlock, isLastInBlock bool) models.ScheduleStopTime {
	stopTime := models.NewScheduleStopTime(
		arrivalMs,
		departureMs,
		utils.FormCombinedID(rowCtx.agencyID, row.ServiceID),
		row.StopHeadsign.String,
		utils.FormCombinedID(rowCtx.agencyID, row.TripID),
	)

	// Blocks start with an arrival.
	if isFirstInBlock {
		stopTime.ArrivalEnabled = false
	}
	// Blocks end with a departure.
	if isLastInBlock {
		stopTime.DepartureEnabled = false
	}

	return stopTime
}

// buildScheduleStopTime converts a schedule row into a ScheduleStopTime, converting GTFS
// times (nanoseconds since midnight) to Unix millisecond timestamps.
func buildScheduleStopTime(row gtfsdb.GetScheduleForStopOnDateRow, rowCtx scheduleRowContext) models.ScheduleStopTime {
	arrivalTimeMs := rowCtx.startOfDay.Add(time.Duration(row.ArrivalTime)).UnixMilli()
	departureTimeMs := rowCtx.startOfDay.Add(time.Duration(row.DepartureTime)).UnixMilli()

	isFirstInBlock, isLastInBlock := blockBoundaries(row, rowCtx.activeServiceBlockTripsMap)
	return newScheduleStopTime(row, rowCtx, arrivalTimeMs, departureTimeMs, isFirstInBlock, isLastInBlock)
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
func addStopTimeToDirectionGroup(acc *routeDirectionAccumulators, dir routeDirection, stopTime models.ScheduleStopTime) {
	if acc.stopTimes[dir.combinedRouteID] == nil {
		acc.stopTimes[dir.combinedRouteID] = make(map[string][]models.ScheduleStopTime)
	}
	acc.stopTimes[dir.combinedRouteID][dir.directionID] = append(acc.stopTimes[dir.combinedRouteID][dir.directionID], stopTime)
}

// expandExactTimesStopTimes expands an exact_times=1 trip's template stop time
// into one ScheduleStopTime per headway offset within its frequency window,
// matching Java OBA. An invalid (non-positive) headway yields nil.
func expandExactTimesStopTimes(row gtfsdb.GetScheduleForStopOnDateRow, freq gtfsdb.Frequency, rowCtx scheduleRowContext) []models.ScheduleStopTime {
	headwayNs := int64(freq.HeadwaySecs) * int64(time.Second)
	if headwayNs <= 0 {
		if rowCtx.logger != nil {
			rowCtx.logger.Warn("schedule-for-stop: skipping frequency trip with invalid headway",
				"trip_id", row.TripID, "headway_secs", freq.HeadwaySecs)
		}
		return nil
	}

	// First stop time of the trip, falling back to this row's arrival.
	var firstStopTime int64
	if row.MinArrivalTime.Valid {
		firstStopTime = row.MinArrivalTime.Int64
	} else {
		firstStopTime = row.ArrivalTime
	}

	arrivalOffset := row.ArrivalTime - firstStopTime
	departureOffset := row.DepartureTime - firstStopTime

	isFirstInBlock, isLastInBlock := blockBoundaries(row, rowCtx.activeServiceBlockTripsMap)

	expanded := make([]models.ScheduleStopTime, 0)
	const maxExpandedStopTimes = 1000
	for offset := int64(0); freq.StartTime+offset < freq.EndTime; offset += headwayNs {
		if len(expanded) >= maxExpandedStopTimes {
			if rowCtx.logger != nil {
				rowCtx.logger.Warn("schedule-for-stop: frequency expansion capped",
					"trip_id", row.TripID, "limit", maxExpandedStopTimes)
			}
			break
		}
		arrivalMs := rowCtx.startOfDay.Add(time.Duration(freq.StartTime + offset + arrivalOffset)).UnixMilli()
		departureMs := rowCtx.startOfDay.Add(time.Duration(freq.StartTime + offset + departureOffset)).UnixMilli()

		stopTime := newScheduleStopTime(row, rowCtx, arrivalMs, departureMs, isFirstInBlock, isLastInBlock)
		expanded = append(expanded, stopTime)
	}

	return expanded
}

// buildScheduleFrequency converts a trip's schedule row and frequency row into a
// ScheduleFrequency, deriving the arrival/departure flags from block position.
func buildScheduleFrequency(row gtfsdb.GetScheduleForStopOnDateRow, freq gtfsdb.Frequency, rowCtx scheduleRowContext) models.ScheduleFrequency {
	isFirstInBlock, isLastInBlock := blockBoundaries(row, rowCtx.activeServiceBlockTripsMap)

	// Java OBA leaves frequency stopHeadsign null.
	return models.NewScheduleFrequencyFromDB(
		freq,
		rowCtx.startOfDay,
		utils.FormCombinedID(rowCtx.agencyID, row.ServiceID),
		utils.FormCombinedID(rowCtx.agencyID, row.TripID),
		"",
		!isFirstInBlock,
		!isLastInBlock,
	)
}

// addFrequencyToDirectionGroup appends scheduleFreq to the route's direction bucket.
func addFrequencyToDirectionGroup(acc *routeDirectionAccumulators, dir routeDirection, scheduleFreq models.ScheduleFrequency) {
	if acc.frequencies[dir.combinedRouteID] == nil {
		acc.frequencies[dir.combinedRouteID] = make(map[string][]models.ScheduleFrequency)
	}
	acc.frequencies[dir.combinedRouteID][dir.directionID] = append(acc.frequencies[dir.combinedRouteID][dir.directionID], scheduleFreq)
}

// frequencyVoteWeight returns headsign votes per frequency row: estimated departures in
// its window (range / headway), per Java OBA, falling back to 1 for bad headways.
func frequencyVoteWeight(freq gtfsdb.Frequency) int {
	if freq.HeadwaySecs <= 0 {
		return 1
	}
	durationSecs := (freq.EndTime - freq.StartTime) / int64(time.Second)
	weight := (durationSecs + freq.HeadwaySecs - 1) / freq.HeadwaySecs
	if weight <= 0 {
		return 1
	}
	return int(weight)
}

// pluralityHeadsign picks the direction's representative tripHeadsign: vote plurality,
// with ties resolved by the alphabetically first headsign.
func pluralityHeadsign(
	routeDirectionHeadsignCounts map[string]map[string]map[string]int,
	combinedRouteID, directionID string,
) string {
	tripHeadsign := ""
	maxCount := 0
	if dirHeadsigns, exists := routeDirectionHeadsignCounts[combinedRouteID][directionID]; exists {
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
	return tripHeadsign
}

// recordHeadsignVote tallies weight votes for a headsign under the route's direction
// bucket; blank headsigns cast no vote.
func recordHeadsignVote(acc *routeDirectionAccumulators, dir routeDirection, headsign sql.NullString, weight int) {
	if !headsign.Valid || headsign.String == "" {
		return
	}

	if weight < 1 {
		weight = 1
	}

	if acc.headsignCounts[dir.combinedRouteID] == nil {
		acc.headsignCounts[dir.combinedRouteID] = make(map[string]map[string]int)
	}
	if acc.headsignCounts[dir.combinedRouteID][dir.directionID] == nil {
		acc.headsignCounts[dir.combinedRouteID][dir.directionID] = make(map[string]int)
	}
	acc.headsignCounts[dir.combinedRouteID][dir.directionID][headsign.String] += weight
}
