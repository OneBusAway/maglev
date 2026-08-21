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
			references = api.buildTripReferences(ctx, tripReferenceParams{IncludeTrip: includeTrip})
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
	serviceDates := newServiceDateResolverFor(todayMidnight, currentTime, serviceIDsByDay{
		QueryDay:    serviceIDs,
		PreviousDay: prevServiceIDs,
	})
	stopIDsMap := make(map[string]string)

	blockTripForRoute, err := api.buildBlockTripForRoute(ctx, fetchedTrips, routeID, serviceIDs, prevServiceIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	situations := newSituationCollector()

	// Indexed so an entry's route and agency resolve without a per-trip query.
	// entryTripID can differ from the active trip on interlined blocks, so the
	// route must come from the entry trip itself.
	tripsByID := make(map[string]gtfsdb.Trip, len(fetchedTrips))
	for _, trip := range fetchedTrips {
		tripsByID[trip.ID] = trip
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
				// Index it too: the entry's situations are looked up by
				// entryTripID, which is this trip, and an unindexed trip sends
				// tripSituationRefs back to the database for what is already here.
				tripsByID[resolution.SelectedTrip.ID] = resolution.SelectedTrip
				if _, known := routeAgencyMap[resolution.SelectedTrip.RouteID]; !known {
					routeAgencyMap[resolution.SelectedTrip.RouteID] = resolution.EntryAgencyID
				}
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

		serviceDate := serviceDates.Resolve(fetchedTrip)

		// Build status from the active trip (tripID). Per spec,
		// status.activeTripId is "the trip the vehicle is currently executing."
		var status *models.TripStatus
		if includeStatus {
			var statusErr error
			status, _, statusErr = api.BuildTripStatus(ctx, activeAgencyID, tripID, nil, serviceDate, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "trip_id", tripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  serviceDate.UnixMilli(),
			SituationIds: situations.addRefs(api.tripSituationRefs(ctx, entryTripID, tripsByID, routeAgencyMap)),
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

		baseTripID, baseTrip := api.resolveDuplicatedBaseTrip(ctx, dupTripID)

		// A DUPLICATED trip with no static counterpart leaves baseTrip zeroed,
		// which the resolver reports as the query day.
		serviceDate := serviceDates.Resolve(baseTrip)

		// Index the base trip before the situation lookup below: an unindexed
		// trip sends tripSituationRefs back to the database for the record
		// already in hand, the same reuse the interlined path above relies on.
		if baseTrip.ID != "" {
			tripsByID[baseTrip.ID] = baseTrip
			if !filteredRouteTrips[baseTripID] {
				fetchedTrips = append(fetchedTrips, baseTrip)
				filteredRouteTrips[baseTripID] = true
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
			status, _, statusErr = api.BuildTripStatus(ctx, agencyID, baseTripID, &vehicle, serviceDate, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed for DUPLICATED trip", "trip_id", baseTripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  serviceDate.UnixMilli(),
			SituationIds: situations.addRefs(api.tripSituationRefs(ctx, baseTripID, tripsByID, routeAgencyMap)),
			TripId:       utils.FormCombinedID(agencyID, dupTripID),
		}
		result = append(result, entry)
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
			stops, err = queryInBatches(ctx, bareIDs, api.GtfsManager.GtfsDB.Queries.GetStopsByIDs)
			if err != nil {
				api.Logger.Warn("failed to fetch stops for references", "error", err, "count", len(bareIDs))
				stops = []gtfsdb.Stop{}
			}
		}

		references = api.buildTripReferences(ctx, tripReferenceParams{
			IncludeTrip:     includeTrip,
			Trips:           result,
			Stops:           stops,
			PreFetchedTrips: fetchedTrips,
			StopIDMap:       stopIDsMap,
			Situations:      situations.refs,
		})
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
		if bt.RouteID == routeID &&
			bt.BlockID.Valid &&
			bt.MinArrivalTime.Valid &&
			bt.MaxDepartureTime.Valid {
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

// tripReferenceParams bundles the inputs the trips-for-route reference block is
// built from, so the builder does not grow another positional parameter each
// time a reference kind is added.
type tripReferenceParams struct {
	IncludeTrip     bool
	Trips           []models.TripsForRouteListEntry
	Stops           []gtfsdb.Stop
	PreFetchedTrips []gtfsdb.Trip
	StopIDMap       map[string]string
	Situations      []situationRef
}

func (api *RestAPI) buildTripReferences(ctx context.Context, params tripReferenceParams) models.ReferencesModel {
	sets := newTripReferenceSets()

	sets.collectPreFetchedTrips(params.PreFetchedTrips)
	sets.collectTripIDsFromEntries(params.Trips)
	api.fillMissingTrips(ctx, sets)

	references := models.NewEmptyReferences()
	var routeIDsByStopID map[string][]string
	references.Stops, routeIDsByStopID = api.stopReferences(ctx, params.Stops, params.StopIDMap)

	for _, combinedRouteIDs := range routeIDsByStopID {
		for _, combinedID := range combinedRouteIDs {
			rawID, err := utils.ExtractCodeID(combinedID)
			if err != nil {
				continue
			}
			if _, exists := sets.routes[rawID]; !exists {
				sets.routes[rawID] = models.Route{}
			}
		}
	}

	api.fillRoutesAndAgencies(ctx, sets)

	references.Agencies = utils.MapValues(sets.agencies)
	references.Routes = sets.routeList()
	references.Trips = sets.tripReferenceList(params.IncludeTrip)
	references.Situations = api.situationReferences(params.Situations)
	return *references
}

// tripSituationRefs resolves a trip's situations from data already loaded,
// falling back to situationRefsForTrip when the trip was not among those
// fetched — DUPLICATED trips with no static counterpart, for instance.
func (api *RestAPI) tripSituationRefs(
	ctx context.Context,
	tripID string,
	tripsByID map[string]gtfsdb.Trip,
	routeAgencyMap map[string]string,
) []situationRef {
	trip, indexed := tripsByID[tripID]
	if !indexed {
		return api.situationRefsForTrip(ctx, tripID)
	}

	// An unknown agency would scope the situation ID to "", emitting the bare
	// alert ID where every other ID in the response is combined-form. The
	// fallback resolves the route and agency itself rather than guessing.
	agencyID, agencyKnown := routeAgencyMap[trip.RouteID]
	if !agencyKnown {
		return api.situationRefsForTrip(ctx, tripID)
	}

	return situationRefsFromAlerts(api.GtfsManager.GetAlertsByIDs(tripID, trip.RouteID, agencyID), agencyID)
}

// tripReferenceSets accumulates the entities a trips-for-route response refers
// to, keyed by bare ID so each is emitted once.
type tripReferenceSets struct {
	trips    map[string]models.Trip
	routes   map[string]models.Route // maps raw route ids to models.Route
	agencies map[string]models.AgencyReference
	// missing holds the trips the response refers to — entry tripIds,
	// schedule.nextTripId/previousTripId, status.activeTripId — whose full
	// records have not been fetched yet. Tracking them explicitly, rather than
	// inferring them from a zero-valued reference, keeps it unambiguous which
	// trips still need a lookup.
	missing map[string]bool
}

func newTripReferenceSets() *tripReferenceSets {
	return &tripReferenceSets{
		trips:    make(map[string]models.Trip),
		routes:   make(map[string]models.Route),
		agencies: make(map[string]models.AgencyReference),
		missing:  make(map[string]bool),
	}
}

// noteTripID records a trip ID that needs a reference, leaving the details to be
// filled in later if they are not known yet.
func (s *tripReferenceSets) noteTripID(combinedID string) {
	_, tripID, err := utils.ExtractAgencyIDAndCodeID(combinedID)
	if err != nil {
		return
	}
	if _, exists := s.trips[tripID]; !exists {
		s.trips[tripID] = models.Trip{}
		s.missing[tripID] = true
	}
}

func (s *tripReferenceSets) collectPreFetchedTrips(trips []gtfsdb.Trip) {
	for _, trip := range trips {
		s.trips[trip.ID] = newTripReference(trip)
		s.routes[trip.RouteID] = models.Route{}
		delete(s.missing, trip.ID)
	}
}

// collectTripIDsFromEntries records every trip an entry points at: its own, the
// adjacent trips in its block, and the trip its vehicle is currently executing.
func (s *tripReferenceSets) collectTripIDsFromEntries(entries []models.TripsForRouteListEntry) {
	for _, entry := range entries {
		s.noteTripID(entry.GetTripId())

		if entry.Schedule != nil {
			s.noteTripID(entry.Schedule.NextTripId)
			s.noteTripID(entry.Schedule.PreviousTripId)
		}
		if entry.Status != nil {
			s.noteTripID(entry.Status.ActiveTripID)
		}
	}
}

// fillMissingTrips loads the trips that were noted by ID but never fetched.
func (api *RestAPI) fillMissingTrips(ctx context.Context, sets *tripReferenceSets) {
	if len(sets.missing) == 0 {
		return
	}

	missingIDs := make([]string, 0, len(sets.missing))
	for id := range sets.missing {
		missingIDs = append(missingIDs, id)
	}

	trips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, missingIDs)
	if err != nil {
		logging.LogError(api.Logger, "failed to fetch trips for references", err)
		return
	}

	sets.collectPreFetchedTrips(trips)
}

// fillRoutesAndAgencies loads every route the collected trips belong to, plus
// the agency owning each of those routes.
func (api *RestAPI) fillRoutesAndAgencies(ctx context.Context, sets *tripReferenceSets) {
	routeIDs := make([]string, 0, len(sets.routes))
	for id := range sets.routes {
		routeIDs = append(routeIDs, id)
	}
	if len(routeIDs) == 0 {
		return
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
	if err != nil {
		logging.LogError(api.Logger, "failed to fetch routes for references", err)
		return
	}

	for _, route := range routes {
		sets.routes[route.ID] = models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String)

		api.addAgencyReference(ctx, sets, route.AgencyID)
	}
}

func (api *RestAPI) addAgencyReference(ctx context.Context, sets *tripReferenceSets, agencyID string) {
	if _, exists := sets.agencies[agencyID]; exists {
		return
	}

	agency, err := api.GtfsManager.FindAgency(ctx, agencyID)
	if err != nil {
		logging.LogError(api.Logger, "failed to fetch agency for references", err, slog.String("agency", agencyID))
		return
	}
	if agency != nil {
		sets.agencies[agency.ID] = models.AgencyReferenceFromDatabase(agency)
	}
}

// tripReferenceList emits the collected trips in combined-ID form. A trip whose
// route was never resolved is skipped, since its agency is unknown.
func (s *tripReferenceSets) tripReferenceList(includeTrip bool) []models.Trip {
	tripsRefList := make([]models.Trip, 0, len(s.trips))
	if !includeTrip {
		return tripsRefList
	}

	for _, trip := range s.trips {
		// A route that was noted but never resolved is still in the map as a
		// zero value; combining IDs against its empty agency would emit
		// references whose every ID is the empty string.
		route, ok := s.routes[trip.RouteID]
		if !ok || route.AgencyID == "" {
			continue
		}
		tripsRefList = append(tripsRefList, models.Trip{
			ID:            utils.FormCombinedID(route.AgencyID, trip.ID),
			RouteID:       utils.FormCombinedID(route.AgencyID, trip.RouteID),
			ServiceID:     utils.FormCombinedID(route.AgencyID, trip.ServiceID),
			TripHeadsign:  trip.TripHeadsign,
			TripShortName: trip.TripShortName,
			DirectionID:   trip.DirectionID,
			BlockID:       utils.FormCombinedID(route.AgencyID, trip.BlockID),
			ShapeID:       utils.FormCombinedID(route.AgencyID, trip.ShapeID),
			PeakOffPeak:   0,
			TimeZone:      "",
		})
	}
	return tripsRefList
}

func (s *tripReferenceSets) routeList() []models.Route {
	routes := make([]models.Route, 0, len(s.routes))
	for _, route := range s.routes {
		if route.ID != "" {
			routes = append(routes, route)
		}
	}
	return routes
}

func newTripReference(trip gtfsdb.Trip) models.Trip {
	return models.Trip{
		ID:            trip.ID,
		RouteID:       trip.RouteID,
		ServiceID:     trip.ServiceID,
		TripHeadsign:  trip.TripHeadsign.String,
		TripShortName: trip.TripShortName.String,
		DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
		BlockID:       trip.BlockID.String,
		ShapeID:       trip.ShapeID.String,
	}
}

// resolveDuplicatedBaseTrip finds the static trip a DUPLICATED real-time trip
// is a run of, returning the ID to use for schedule and status lookups together
// with the trip row itself.
//
// The full ID is tried first, then the ID with a trailing numeric suffix
// stripped, which is how some feeds distinguish duplicated runs. The stripped
// ID is adopted only once it resolves: handing on an ID that matches no trip is
// worse than keeping the unresolvable one the feed sent. When neither resolves,
// the trip comes back zeroed, which the service date resolver reports as the
// query day.
func (api *RestAPI) resolveDuplicatedBaseTrip(ctx context.Context, dupTripID string) (string, gtfsdb.Trip) {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, dupTripID)
	if err == nil {
		return dupTripID, trip
	}
	if !errors.Is(err, sql.ErrNoRows) {
		api.Logger.Warn("trips-for-route: failed to resolve DUPLICATED trip ID",
			"dup_trip_id", dupTripID, "error", err)
	}

	stripped := stripNumericSuffix(dupTripID)
	if stripped == dupTripID {
		return dupTripID, gtfsdb.Trip{}
	}

	strippedTrip, strippedErr := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, stripped)
	if strippedErr != nil {
		if !errors.Is(strippedErr, sql.ErrNoRows) {
			api.Logger.Warn("trips-for-route: failed to resolve stripped DUPLICATED trip ID",
				"dup_trip_id", dupTripID, "stripped_trip_id", stripped, "error", strippedErr)
		}
		return dupTripID, gtfsdb.Trip{}
	}
	return stripped, strippedTrip
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
