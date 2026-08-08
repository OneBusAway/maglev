package restapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
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

	query := r.URL.Query()
	includeSchedule := parseBoolQueryParam(query, "includeSchedule")
	includeStatus := parseBoolQueryParam(query, "includeStatus")
	includeTrip := parseIncludeTrip(query)
	includeReferences := ShouldIncludeReferences(r)

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			references := models.NewEmptyReferences()
			response := models.NewListResponse([]models.TripsForRouteListEntry{}, *references, false, api.Clock)
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
	formattedDate, currentTime, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation, api.Clock)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Time since midnight of the service day, as a duration.
	serviceDayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location())
	currentSinceMidnight := max(currentTime.Sub(serviceDayMidnight), 0)

	// Check the previous day's service for trips running past midnight.
	// GTFS allows departure times > 24:00:00 (e.g., 25:30:00 = 1:30 AM next day).
	// These trips belong to yesterday's service but are still active now.
	// TODO: We should add config for runningLateWindow and runningEarlyWindow like Java OBA
	// source:https://groups.google.com/g/onebusaway-developers/c/j-G-1UyfbXI/m/J-Su3BArKW0J
	const (
		runningLate  = 30 * time.Minute // runningLateWindow
		runningEarly = 10 * time.Minute // runningEarlyWindow
	)
	prevDay := currentTime.AddDate(0, 0, -1)
	prevFormattedDate := prevDay.Format("20060102")
	prevServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, prevFormattedDate)
	if err != nil {
		api.Logger.Warn("trips-for-route: failed to fetch previous-day service IDs", "date", prevFormattedDate, "error", err)
		prevServiceIDs = nil
	}
	// I'm confused by adding 24 hours to get the previous day here, but that's the existing behavior.
	prevDaySinceMidnight := currentSinceMidnight + (24 * time.Hour)

	indexIDs, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
		RouteID:    routeID,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Match Java OBA: look back 30 min (catch late vehicles) and ahead 10 min (catch early vehicles).
	timeRangeStart := currentSinceMidnight - runningLate
	timeRangeEnd := currentSinceMidnight + runningEarly

	layoverBlocks, err := api.GtfsManager.GtfsDB.Queries.GetActiveLayoverBlockIDsForRoute(ctx, gtfsdb.GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     serviceIDs,
		TimeRangeStart: timeRangeStart.Nanoseconds(),
		TimeRangeEnd:   timeRangeEnd.Nanoseconds(),
	})
	if err != nil {
		api.Logger.Warn("trips-for-route: failed to fetch layover blocks", "route_id", routeID, "error", err)
		layoverBlocks = nil
	}

	allLinkedBlocks := make(map[string]bool)

	if len(indexIDs) > 0 {
		blocksFromIndices, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
			FromTime:   sql.NullInt64{Int64: timeRangeStart.Nanoseconds(), Valid: true},
			ToTime:     sql.NullInt64{Int64: timeRangeEnd.Nanoseconds(), Valid: true},
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

	// Find blocks from previous day's service (for trips running past midnight).
	if len(prevServiceIDs) > 0 {
		prevIndexIDs, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
			RouteID:    routeID,
			ServiceIds: prevServiceIDs,
		})
		if err != nil {
			api.Logger.Warn("trips-for-route: failed to fetch previous-day block index IDs", "error", err)
		} else if len(prevIndexIDs) > 0 {
			prevFromTime := prevDaySinceMidnight + timeRangeStart - currentSinceMidnight
			prevToTime := prevDaySinceMidnight + timeRangeEnd - currentSinceMidnight
			prevBlocks, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
				FromTime:   sql.NullInt64{Int64: prevFromTime.Nanoseconds(), Valid: true},
				ToTime:     sql.NullInt64{Int64: prevToTime.Nanoseconds(), Valid: true},
				IndexIds:   prevIndexIDs,
				ServiceIds: prevServiceIDs,
			})
			if err != nil {
				api.Logger.Warn("trips-for-route: failed to fetch previous-day blocks", "error", err)
			} else {
				for _, b := range prevBlocks {
					if b.Valid {
						allLinkedBlocks[b.String] = true
					}
				}
			}
		}
	}

	nullBlockTrips, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
		RouteID:        routeID,
		ServiceIds:     serviceIDs,
		TimeRangeStart: sql.NullInt64{Int64: timeRangeStart.Nanoseconds(), Valid: true},
		TimeRangeEnd:   sql.NullInt64{Int64: timeRangeEnd.Nanoseconds(), Valid: true},
	})
	if err != nil {
		api.Logger.Warn("trips-for-route: failed to fetch null-block trips", "route_id", routeID, "error", err)
		nullBlockTrips = nil
	}

	if len(prevServiceIDs) > 0 {
		prevNullBlockTrips, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
			RouteID:        routeID,
			ServiceIds:     prevServiceIDs,
			TimeRangeStart: sql.NullInt64{Int64: (prevDaySinceMidnight + timeRangeStart - currentSinceMidnight).Nanoseconds(), Valid: true},
			TimeRangeEnd:   sql.NullInt64{Int64: (prevDaySinceMidnight + timeRangeEnd - currentSinceMidnight).Nanoseconds(), Valid: true},
		})
		if err != nil {
			api.Logger.Warn("trips-for-route: failed to fetch previous-day null-block trips", "error", err)
		} else {
			nullBlockTrips = append(nullBlockTrips, prevNullBlockTrips...)
		}
	}

	if len(allLinkedBlocks) == 0 && len(nullBlockTrips) == 0 {
		var references models.ReferencesModel
		if includeReferences {
			references = buildTripReferences(api, ctx, includeTrip, []models.TripsForRouteListEntry{}, []gtfsdb.Stop{}, nil, nil)
		} else {
			references = *models.NewEmptyReferences()
		}
		response := models.NewListResponse([]models.TripsForRouteListEntry{}, references, false, api.Clock)
		api.sendResponse(w, r, response)
		return
	}

	var activeTrips []string

	type serviceDayEntry struct {
		serviceIDs    []string
		sinceMidnight time.Duration
	}
	serviceDays := []serviceDayEntry{
		{serviceIDs: serviceIDs, sinceMidnight: currentSinceMidnight},
	}
	if len(prevServiceIDs) > 0 {
		serviceDays = append(serviceDays, serviceDayEntry{
			serviceIDs:    prevServiceIDs,
			sinceMidnight: prevDaySinceMidnight,
		})
	}

	for blockID := range allLinkedBlocks {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		blockIDNullStr := nulls.String(blockID)

		for _, sd := range serviceDays {
			tripsInBlock, err := api.GtfsManager.GtfsDB.Queries.GetTripsInBlock(ctx, gtfsdb.GetTripsInBlockParams{
				BlockID:    blockIDNullStr,
				ServiceIds: sd.serviceIDs,
			})
			if err != nil {
				api.Logger.Warn("trips-for-route: failed to fetch trips in block", "block_id", blockID, "error", err)
				continue
			}
			if len(tripsInBlock) == 0 {
				continue
			}

			activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripInBlockAtTime(ctx, gtfsdb.GetActiveTripInBlockAtTimeParams{
				BlockID:     blockIDNullStr,
				ServiceIds:  sd.serviceIDs,
				CurrentTime: sql.NullInt64{Int64: sd.sinceMidnight.Nanoseconds(), Valid: true}})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				api.Logger.Warn("trips-for-route: failed to get active trip in block", "block_id", blockID, "error", err)
				continue
			}
			if errors.Is(err, sql.ErrNoRows) {
				// No trip in this block is currently running at the requested time.
				// Java OBA only returns blocks with a currently-running trip (see
				// BlockStatusServiceImpl.computeLocations which adds scheduled locations
				// only when isInService()). Skip rather than picking a "best candidate"
				// upcoming/past trip that isn't actually running.
				continue
			}

			activeTrips = append(activeTrips, activeTrip)
			break
		}
	}

	activeTrips = append(activeTrips, nullBlockTrips...)

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
	routeAgencyMap := make(map[string]string)
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

		for _, route := range routes {
			routeAgencyMap[route.ID] = route.AgencyID
		}
		for _, trip := range fetchedTrips {
			if aID, ok := routeAgencyMap[trip.RouteID]; ok {
				tripAgencyMap[trip.ID] = aID
			}
		}
	}

	todayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	stopIDsMap := make(map[string]string)

	blockTripForRoute, err := api.buildBlockTripForRoute(ctx, fetchedTrips, routeID, serviceIDs, prevServiceIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	var result []models.TripsForRouteListEntry
	for _, fetchedTrip := range fetchedTrips {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		tripID := fetchedTrip.ID

		activeAgencyID, ok := tripAgencyMap[tripID]
		if !ok {
			continue
		}

		// Determine the entry's trip identity. For interlined blocks where the
		// active trip is on another route, entryTripID is the queried-route trip
		// in this block whose time window is nearest to the active trip — i.e.
		// "the trip on the queried route that caused this block to be selected."
		// status.activeTripId will still reflect the vehicle's current trip.
		entryTripID := tripID
		entryAgencyID := activeAgencyID
		if fetchedTrip.RouteID != routeID && fetchedTrip.BlockID.Valid {
			if resolution, resolved := resolveInterlinedEntryTripID(fetchedTrip, routeID, agencyID, blockTripForRoute, routeAgencyMap); resolved {
				entryTripID = resolution.EntryTripID
				entryAgencyID = resolution.EntryAgencyID
				// Keep the selected queried-route trip available when
				// building references so the entry's trip reference (route,
				// headsign, ...) reflects the entry's tripId rather than the
				// active trip's.
				fetchedTrips = append(fetchedTrips, resolution.SelectedTrip)
			}
			// If unresolved (no queried-route trip exists anywhere in this
			// block), entryTripID/entryAgencyID keep their active-trip
			// defaults above. This matches legacy OBA, which always reports
			// the active trip's own ID here, and preserves the one-entry-
			// per-active-block guarantee rather than dropping the entry.
		}

		// Build schedule from entryTripID (the entry's own trip), not the active
		// trip. Per spec, schedule.stopTimes is "scheduled stop times for this
		// trip" and schedule.previousTripId is "the preceding trip in this
		// vehicle's block" — both relative to the entry's trip identity.
		var schedule *models.TripsSchedule
		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, entryTripID, entryAgencyID, currentTime, currentLocation)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}

			collectStopIDsFromSchedule(schedule, stopIDsMap)
		}

		// Build status from the active trip (tripID). Per spec,
		// status.activeTripId is "the trip the vehicle is currently executing."
		var status *models.TripStatus
		if includeStatus {
			var statusErr error
			status, _, statusErr = api.BuildTripStatus(ctx, activeAgencyID, tripID, nil, todayMidnight, currentTime)
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
			SituationIds: api.GetSituationIDsForTrip(r.Context(), entryTripID),
			TripId:       utils.FormCombinedID(entryAgencyID, entryTripID),
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
			status, _, statusErr = api.BuildTripStatus(ctx, agencyID, baseTripID, &vehicle, todayMidnight, currentTime)
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
			bareIDs := make([]string, 0, len(stopIDsMap))
			for bareID := range stopIDsMap {
				bareIDs = append(bareIDs, bareID)
			}
			var err error
			stops, err = api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, bareIDs)
			if err != nil {
				api.Logger.Warn("failed to fetch stops for references", "error", err, "count", len(bareIDs))
				stops = []gtfsdb.Stop{}
			}
		}

		references = buildTripReferences(api, ctx, includeTrip, result, stops, fetchedTrips, stopIDsMap)
	} else {
		references = *models.NewEmptyReferences()
	}
	response := models.NewListResponse(result, references, false, api.Clock)
	api.sendResponse(w, r, response)
}

// blockTripEntry is a candidate queried-route trip within an interlined
// block, carrying just enough of its schedule window to pick the one nearest
// a given active trip.
type blockTripEntry struct {
	ID               string
	MinArrivalTime   int64
	MaxDepartureTime int64
	Trip             gtfsdb.Trip
}

// buildBlockTripForRoute batch-fetches every trip in the blocks that
// fetchedTrips are interlined through (i.e. an active trip on another route
// sharing a block with the queried route), and returns, per block ID, the
// queried-route trips found in it. Only today's and yesterday's active
// service IDs are considered, matching the rest of this handler's active-trip
// resolution window.
func (api *RestAPI) buildBlockTripForRoute(
	ctx context.Context,
	fetchedTrips []gtfsdb.Trip,
	routeID string,
	serviceIDs, prevServiceIDs []string,
) (map[string][]blockTripEntry, error) {
	blockTripForRoute := make(map[string][]blockTripEntry)

	var interlinedBlockIDs []sql.NullString
	for _, t := range fetchedTrips {
		if t.RouteID != routeID && t.BlockID.Valid {
			interlinedBlockIDs = append(interlinedBlockIDs, t.BlockID)
		}
	}
	if len(interlinedBlockIDs) == 0 {
		return blockTripForRoute, nil
	}

	// Explicit copy: append(serviceIDs, ...) could alias serviceIDs' backing
	// array if it has spare capacity, which would mutate serviceIDs.
	allServiceIDs := make([]string, len(serviceIDs))
	copy(allServiceIDs, serviceIDs)
	if len(prevServiceIDs) > 0 {
		allServiceIDs = append(allServiceIDs, prevServiceIDs...)
	}

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDs(ctx, gtfsdb.GetTripsByBlockIDsParams{
		BlockIds:   interlinedBlockIDs,
		ServiceIds: allServiceIDs,
	})
	if err != nil {
		return nil, err
	}

	for _, bt := range blockTrips {
		// MinArrivalTime/MaxDepartureTime are NULL for a trip with no
		// stop_times (see schema.sql); such a trip has no time window to
		// compare against, so it can't be a nearest-midpoint candidate.
		if bt.RouteID == routeID && bt.BlockID.Valid && bt.MinArrivalTime.Valid && bt.MaxDepartureTime.Valid {
			key := bt.BlockID.String
			blockTripForRoute[key] = append(blockTripForRoute[key], blockTripEntry{
				ID:               bt.ID,
				MinArrivalTime:   bt.MinArrivalTime.Int64,
				MaxDepartureTime: bt.MaxDepartureTime.Int64,
				Trip:             tripsByBlockIDsRowToTrip(bt),
			})
		}
	}
	return blockTripForRoute, nil
}

// interlinedTripResolution is the outcome of resolving an entry's trip
// identity when its active trip belongs to a different route than the one
// queried.
type interlinedTripResolution struct {
	EntryTripID   string
	EntryAgencyID string
	SelectedTrip  gtfsdb.Trip
}

// resolveInterlinedEntryTripID finds, among the queried-route trips sharing
// fetchedTrip's block (as built by buildBlockTripForRoute), the one whose
// time window is nearest to fetchedTrip's own — i.e. "the trip on the
// queried route that caused this block to be selected." Block trips are
// sequential (one vehicle) and never overlap in time, so nearest-midpoint is
// used instead of an overlap test, which would fail across any layover gap.
// Keying blockTripForRoute on block alone (not block+service) matters
// because a block's trips aren't guaranteed to share one literal service_id:
// GTFS allows two service_ids to be simultaneously active on the same
// calendar day, and nothing requires a block's trips to agree on which one
// they're tagged with.
//
// A block ID can also be reused across otherwise-unrelated service_ids (e.g.
// an agency reusing block "101" for both yesterday's and today's schedule),
// which would let a same-block candidate from the wrong calendar day win a
// nearest-midpoint search purely by time-of-day coincidence. Candidates that
// share fetchedTrip's exact service_id are preferred first: trips under one
// service_id recur together on every date that service_id is active, so
// they can never be a cross-day collision. The broader nearest-midpoint
// search across all candidates remains as a fallback for the legitimate
// case of two distinct service_ids both active on the same calendar day.
//
// ok is false if no queried-route trip exists anywhere in the block.
func resolveInterlinedEntryTripID(
	fetchedTrip gtfsdb.Trip,
	routeID, agencyID string,
	blockTripForRoute map[string][]blockTripEntry,
	routeAgencyMap map[string]string,
) (result interlinedTripResolution, ok bool) {
	entries := blockTripForRoute[fetchedTrip.BlockID.String]
	if len(entries) == 0 {
		return interlinedTripResolution{}, false
	}
	if !fetchedTrip.MinArrivalTime.Valid || !fetchedTrip.MaxDepartureTime.Valid {
		return interlinedTripResolution{}, false
	}

	if sameService := entriesWithServiceID(entries, fetchedTrip.ServiceID); len(sameService) > 0 {
		entries = sameService
	}

	activeMid := (fetchedTrip.MinArrivalTime.Int64 + fetchedTrip.MaxDepartureTime.Int64) / 2
	bestIdx := 0
	bestDist := int64(-1)
	for i, e := range entries {
		eMid := (e.MinArrivalTime + e.MaxDepartureTime) / 2
		dist := eMid - activeMid
		if dist < 0 {
			dist = -dist
		}
		if bestDist == -1 || dist < bestDist {
			bestDist = dist
			bestIdx = i
		}
	}

	entryAgencyID := agencyID
	if queriedAgency, ok := routeAgencyMap[routeID]; ok {
		entryAgencyID = queriedAgency
	}

	return interlinedTripResolution{
		EntryTripID:   entries[bestIdx].ID,
		EntryAgencyID: entryAgencyID,
		SelectedTrip:  entries[bestIdx].Trip,
	}, true
}

// entriesWithServiceID returns the subset of entries whose trip runs under
// serviceID.
func entriesWithServiceID(entries []blockTripEntry, serviceID string) []blockTripEntry {
	matches := make([]blockTripEntry, 0, len(entries))
	for _, e := range entries {
		if e.Trip.ServiceID == serviceID {
			matches = append(matches, e)
		}
	}
	return matches
}

func tripsByBlockIDsRowToTrip(row gtfsdb.GetTripsByBlockIDsRow) gtfsdb.Trip {
	return gtfsdb.Trip{
		ID:               row.ID,
		RouteID:          row.RouteID,
		ServiceID:        row.ServiceID,
		TripHeadsign:     row.TripHeadsign,
		TripShortName:    row.TripShortName,
		DirectionID:      row.DirectionID,
		BlockID:          row.BlockID,
		ShapeID:          row.ShapeID,
		MinArrivalTime:   row.MinArrivalTime,
		MaxDepartureTime: row.MaxDepartureTime,
	}
}

func collectStopIDsFromSchedule(schedule *models.TripsSchedule, stopIDsMap map[string]string) {
	if schedule == nil {
		return
	}
	for _, stopTime := range schedule.StopTimes {
		_, bareID, err := utils.ExtractAgencyIDAndCodeID(stopTime.StopID)
		if err == nil {
			if _, exists := stopIDsMap[bareID]; !exists {
				stopIDsMap[bareID] = stopTime.StopID
			}
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
	stopIDMap map[string]string,
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
			ID:                 stopIDMap[stop.ID],
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

// parseBoolQueryParam parses a boolean query parameter, defaulting to true when
// the parameter is omitted and to false when present but not a valid boolean.
func parseBoolQueryParam(query url.Values, name string) bool {
	if !query.Has(name) {
		return true
	}
	val, err := strconv.ParseBool(query.Get(name))
	return err == nil && val
}
