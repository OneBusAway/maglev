package restapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

type tripsForRouteServiceDay struct {
	blockIDs      []string
	serviceIDs    []string
	sinceMidnight time.Duration
	midnight      time.Time
}

// tripsForRouteHandler returns all active trips for a route, including their real-time
// status, schedule, and vehicle positions when available.
func (api *RestAPI) tripsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	reqLogger := logging.ForComponent(r.Context(), "http_server")

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
	_, currentTime, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation, api.Clock)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencies, err := api.GtfsManager.GetAgencies(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	// An interlined trip can belong to an agency on another local date, so resolve every agency's zone.
	serviceDatesByZone, agencyLocations, err := api.routeZoneServiceDates(ctx, agencies, currentAgency.ID, currentLocation, currentTime)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	routeServiceDays := serviceDatesByZone[currentLocation.String()].ServiceDays()

	// tripServiceDay records the service-day midnight each active trip was found under.
	tripServiceDay := make(map[string]time.Time)

	dayBlockIDs := make([][]string, len(routeServiceDays))
	var nullBlockTrips []string
	for dayIndex, day := range routeServiceDays {
		blockIDs, dayNullBlockTrips, err := api.routeBlocksAndTripsInService(ctx, routeID, day)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		dayBlockIDs[dayIndex] = blockIDs
		for _, id := range dayNullBlockTrips {
			if _, found := tripServiceDay[id]; !found {
				tripServiceDay[id] = day.midnight
				nullBlockTrips = append(nullBlockTrips, id)
			}
		}
	}

	hasBlocks := slices.ContainsFunc(dayBlockIDs, func(blockIDs []string) bool { return len(blockIDs) > 0 })
	if !hasBlocks && len(nullBlockTrips) == 0 {
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

	// Reuse the snapshot when BuildTripStatus processes the selected trip below.
	// Resolving a block and building its status otherwise perform the same four
	// database queries independently.
	ctx = WithSnapshotCache(ctx, newSnapshotCache())

	// A block resolves on the service day its route trip was found on, so a block ID
	// reused on both days can yield one active trip per day.
	var activeTrips []string
	for dayIndex, day := range routeServiceDays {
		dayActiveTrips, err := api.activeTripsInBlocks(ctx, dayBlockIDs[dayIndex], dayIndex, day,
			currentLocation.String(), serviceDatesByZone, agencyLocations, currentTime)
		if err != nil {
			if ctx.Err() != nil {
				api.clientCanceledResponse(w, r, ctx.Err())
				return
			}
			api.serverErrorResponse(w, r, err)
			return
		}
		for tripID, serviceDayMidnight := range dayActiveTrips {
			if _, found := tripServiceDay[tripID]; found {
				continue
			}
			tripServiceDay[tripID] = serviceDayMidnight
			activeTrips = append(activeTrips, tripID)
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

	// Batch-fetch frequencies; success seeds nil to skip fallback queries.
	// Post-batch trips (interlined, DUPLICATED) fall back per-trip.
	freqMap := make(map[string][]gtfsdb.Frequency)
	if len(fetchedTrips) > 0 {
		tripIDsForFreq := make([]string, 0, len(fetchedTrips))
		for _, trip := range fetchedTrips {
			tripIDsForFreq = append(tripIDsForFreq, trip.ID)
		}
		var freqErr error
		freqMap, freqErr = api.fetchFrequenciesForTrips(ctx, tripIDsForFreq)
		if freqErr != nil {
			api.serverErrorResponse(w, r, freqErr)
			return
		}
	}

	routeToday, routeYesterday := routeServiceDays[0], routeServiceDays[1]
	blockTripForRoute, err := api.buildBlockTripForRoute(ctx, fetchedTrips, routeID, routeToday.serviceIDs, routeYesterday.serviceIDs)
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

		entryLocation := locationOrDefault(agencyLocations, entryAgencyID, currentLocation)
		entryServiceDate := serviceDatesByZone[entryLocation.String()].Resolve(tripsByID[entryTripID])
		activeLocation := locationOrDefault(agencyLocations, activeAgencyID, currentLocation)
		activeServiceDate := serviceDatesByZone[activeLocation.String()].Resolve(fetchedTrip)

		var schedule *models.TripsSchedule
		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, entryTripID, entryAgencyID, entryServiceDate, entryLocation, freqMap)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}
		}

		var status *models.TripStatus
		if includeStatus {
			var statusErr error
			status, _, statusErr = api.BuildTripStatus(ctx, activeAgencyID, tripID, nil, activeServiceDate, currentTime, freqMap)
			if statusErr != nil {
				reqLogger.Warn("BuildTripStatus failed", "trip_id", tripID, "error", statusErr)
				status = nil
			}
		}

		frequency, freqErr := api.frequencyForEntry(ctx, freqMap, entryTripID, entryServiceDate, currentTime)
		if freqErr != nil {
			api.serverErrorResponse(w, r, freqErr)
			return
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    frequency,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  entryServiceDate.UnixMilli(),
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

		// Fetch the base trip once; its stop-time window drives the
		// service-date resolution below.
		baseTripID, baseTrip, err := api.resolveDuplicatedBaseTrip(ctx, dupTripID)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		resolved := baseTrip.ID != ""

		// Index the base trip before the situation lookup below: an unindexed
		// trip sends tripSituationRefs back to the database for the record
		// already in hand, the same reuse the interlined path above relies on.
		if resolved {
			tripsByID[baseTrip.ID] = baseTrip
			if !filteredRouteTrips[baseTripID] {
				fetchedTrips = append(fetchedTrips, baseTrip)
				filteredRouteTrips[baseTripID] = true
			}
		}

		serviceDate := serviceDateFor(tripServiceDay, baseTripID, routeToday.midnight)
		// If the base trip's window overlaps yesterday's range, use yesterday.
		if serviceDate.Equal(routeToday.midnight) && resolved &&
			tripWindowOverlapsRange(baseTrip,
				time.Duration(routeYesterday.sinceMidnightNs)-runningLate,
				time.Duration(routeYesterday.sinceMidnightNs)+runningEarly) {
			serviceDate = routeYesterday.midnight
		}

		var schedule *models.TripsSchedule
		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, baseTripID, agencyID, serviceDate, currentLocation, freqMap)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}
		}

		var status *models.TripStatus
		if includeStatus {
			var statusErr error
			status, _, statusErr = api.BuildTripStatus(ctx, agencyID, baseTripID, &vehicle, serviceDate, currentTime, freqMap)
			if statusErr != nil {
				reqLogger.Warn("BuildTripStatus failed for DUPLICATED trip", "trip_id", baseTripID, "error", statusErr)
				status = nil
			}
		}

		frequency, freqErr := api.frequencyForEntry(ctx, freqMap, baseTripID, serviceDate, currentTime)
		if freqErr != nil {
			api.serverErrorResponse(w, r, freqErr)
			return
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    frequency,
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
		tripSchedulesAndStatuses := make([]tripScheduleAndStatus, 0, len(result))

		for _, trip := range result {
			tripSchedulesAndStatuses = append(tripSchedulesAndStatuses, tripScheduleAndStatus{
				schedule: trip.Schedule,
				status:   trip.Status,
			})
		}
		// stop ids map maps stopIDs to a slice of unique combined agency IDs.
		stopsReferenced, stopIDsMap, stopsErr := api.stopsReferencedBySchedulesAndStatuses(ctx, tripSchedulesAndStatuses)
		if stopsErr != nil {
			api.serverErrorResponse(w, r, stopsErr)
			return
		}

		references = api.buildTripReferences(ctx, tripReferenceParams{
			IncludeTrip:     includeTrip,
			Trips:           result,
			Stops:           stopsReferenced,
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

// resolveTripsForRouteBlocks returns the Java-style scheduled active trip for
// every selected block. A block remains in service between two consecutive
// trips: its scheduled position is interpolated across that gap, and the
// distance along the block determines whether the previous or next trip is
// active. This is deliberately the same snapshot path used by
// trips-for-location and BuildTripStatus.
func (api *RestAPI) resolveTripsForRouteBlocks(
	ctx context.Context,
	serviceDays []tripsForRouteServiceDay,
	currentTime time.Time,
) ([]string, map[string]time.Time, error) {
	var activeTrips []string
	tripServiceDays := make(map[string]time.Time)

	for _, sd := range serviceDays {
		if len(sd.blockIDs) == 0 || len(sd.serviceIDs) == 0 {
			continue
		}

		spans, err := api.tripsForRouteBlockSpans(ctx, sd)
		if err != nil {
			return nil, nil, err
		}

		anchors := tripsForRouteBlockAnchors(spans, sd.sinceMidnight)
		dayTrips, err := api.resolveTripsForRouteAnchors(ctx, anchors, currentTime, sd.midnight)
		if err != nil {
			return nil, nil, err
		}
		for _, tripID := range dayTrips {
			// A trip ID can recur on both adjacent service days. Preserve the
			// current-day result, which is processed first, while still allowing
			// distinct trip IDs from a reused block to be returned.
			if _, alreadyResolved := tripServiceDays[tripID]; alreadyResolved {
				continue
			}
			activeTrips = append(activeTrips, tripID)
			tripServiceDays[tripID] = sd.midnight
		}
	}

	return activeTrips, tripServiceDays, nil
}

// tripsForRouteBlockSpans batch-fetches the scheduled spans for one service
// day's candidate blocks.
func (api *RestAPI) tripsForRouteBlockSpans(
	ctx context.Context,
	sd tripsForRouteServiceDay,
) ([]gtfsdb.GetTripSpansForBlocksRow, error) {
	return queryInBatchesReserving(ctx, sd.blockIDs, len(sd.serviceIDs),
		func(ctx context.Context, batch []string) ([]gtfsdb.GetTripSpansForBlocksRow, error) {
			nullableBatch := make([]sql.NullString, len(batch))
			for i, blockID := range batch {
				nullableBatch[i] = nulls.String(blockID)
			}
			return api.GtfsManager.GtfsDB.Queries.GetTripSpansForBlocks(ctx, gtfsdb.GetTripSpansForBlocksParams{
				BlockIds:   nullableBatch,
				ServiceIds: sd.serviceIDs,
			})
		})
}

// tripsForRouteBlockAnchors filters invalid spans, restores global ordering
// after batched queries, and selects the relevant anchor from each block.
func tripsForRouteBlockAnchors(
	spans []gtfsdb.GetTripSpansForBlocksRow,
	sinceMidnight time.Duration,
) []blockAnchor {
	validSpans := spans[:0]
	for _, span := range spans {
		if span.BlockID.Valid && span.MinArrivalTime.Valid && span.MaxDepartureTime.Valid {
			validSpans = append(validSpans, span)
		}
	}
	slices.SortFunc(validSpans, compareTripsForRouteBlockSpans)

	windowStart := sinceMidnight.Nanoseconds() - int64(runningLate)
	windowEnd := sinceMidnight.Nanoseconds() + int64(runningEarly)
	return selectBlockAnchors(validSpans, windowStart, windowEnd)
}

// compareTripsForRouteBlockSpans orders spans by block, start time, and trip
// ID. Batched query results are individually sorted but must be merged here.
func compareTripsForRouteBlockSpans(a, b gtfsdb.GetTripSpansForBlocksRow) int {
	if byBlock := strings.Compare(a.BlockID.String, b.BlockID.String); byBlock != 0 {
		return byBlock
	}
	if a.MinArrivalTime.Int64 < b.MinArrivalTime.Int64 {
		return -1
	}
	if a.MinArrivalTime.Int64 > b.MinArrivalTime.Int64 {
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}

// resolveTripsForRouteAnchors validates scheduled snapshots and emits each
// block at most once within this service day. The same block ID may represent
// a separate active run on another service day and is intentionally not
// deduplicated across calls.
func (api *RestAPI) resolveTripsForRouteAnchors(
	ctx context.Context,
	anchors []blockAnchor,
	currentTime, serviceDate time.Time,
) ([]string, error) {
	activeTrips := make([]string, 0, len(anchors))
	resolvedBlocks := make(map[string]struct{}, len(anchors))

	for _, anchor := range anchors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, alreadyResolved := resolvedBlocks[anchor.blockID]; alreadyResolved {
			continue
		}

		snapshot := api.computeScheduledBlockSnapshot(ctx, anchor.tripID, currentTime, serviceDate)
		if snapshot == nil || !snapshot.InRange || snapshot.ActiveTripID == "" {
			continue
		}

		activeTrips = append(activeTrips, snapshot.ActiveTripID)
		resolvedBlocks[anchor.blockID] = struct{}{}
	}
	return activeTrips, nil
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

// tripServiceDayMidnight returns midnight of the trip's service day in the
// agency's timezone. Overnight trips running under yesterday's (request-frame)
// service get that date; all other trips get the request's date.
func tripServiceDayMidnight(currentTime time.Time, trip *gtfsdb.Trip, agencyLocation *time.Location, serviceIDs, prevServiceIDs []string) time.Time {
	serviceDate := currentTime
	if !slices.Contains(serviceIDs, trip.ServiceID) && slices.Contains(prevServiceIDs, trip.ServiceID) {
		serviceDate = currentTime.AddDate(0, 0, -1)
	}
	return time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, agencyLocation)
}

func collectStopIDsFromSchedule(schedule *models.TripsSchedule, stopIDsMap map[string][]string) {
	if schedule == nil {
		return
	}
	for _, stopTime := range schedule.StopTimes {
		_, bareID, err := utils.ExtractAgencyIDAndCodeID(stopTime.StopID)
		if err == nil {
			appendUniqueStopID(stopIDsMap, bareID, stopTime.StopID)
		}
	}
}

// serviceDateFor returns the service-day midnight recorded for id, falling
// back to todayMidnight.
func serviceDateFor(tripServiceDay map[string]time.Time, id string, todayMidnight time.Time) time.Time {
	if midnight, ok := tripServiceDay[id]; ok {
		return midnight
	}
	return todayMidnight
}

// activeTripsInBlocks returns the trip each block is running at the request time,
// keyed to its service-day midnight. blockIDs were found on routeDay, the
// dayIndex entry of the route agency's service days, and a candidate is only
// tested against the same entry for its own agency's timezone.
// routeZoneServiceDates resolves the queried route agency's zone first, then every
// other agency zone best effort. An agency whose time zone cannot be loaded, or whose
// service days cannot be read, is logged and left out: a route that does not depend on
// that agency still answers.
func (api *RestAPI) routeZoneServiceDates(
	ctx context.Context,
	agencies []gtfsdb.Agency,
	currentAgencyID string,
	currentLocation *time.Location,
	currentTime time.Time,
) (map[string]*serviceDateResolver, map[string]*time.Location, error) {
	reqLogger := logging.ForComponent(ctx, "http_server")

	routeResolver, err := api.serviceDateResolverForZone(ctx, currentLocation, currentTime)
	if err != nil {
		return nil, nil, err
	}
	resolvers := map[string]*serviceDateResolver{currentLocation.String(): routeResolver}
	locations := map[string]*time.Location{currentAgencyID: currentLocation}

	for _, agency := range agencies {
		if agency.ID == currentAgencyID {
			continue
		}
		location, err := loadAgencyLocation(agency.ID, agency.Timezone)
		if err != nil {
			reqLogger.Warn("trips-for-route: skipping agency with an unusable time zone",
				"agencyID", agency.ID, "timezone", agency.Timezone, "error", err)
			continue
		}
		locations[agency.ID] = location

		if _, resolved := resolvers[location.String()]; resolved {
			continue
		}
		resolver, err := api.serviceDateResolverForZone(ctx, location, currentTime)
		if err != nil {
			reqLogger.Warn("trips-for-route: skipping agency zone with no service days",
				"agencyID", agency.ID, "zone", location.String(), "error", err)
			continue
		}
		resolvers[location.String()] = resolver
	}

	return resolvers, locations, nil
}

// serviceDateResolverForZone builds one zone's resolver. The previous day's service IDs
// stay best effort, as they were before this handler resolved dates per zone: losing
// them costs past-midnight trips rather than the response.
func (api *RestAPI) serviceDateResolverForZone(
	ctx context.Context,
	location *time.Location,
	currentTime time.Time,
) (*serviceDateResolver, error) {
	queryDayMidnight := serviceDateMidnight(currentTime, location)
	queryDay, err := api.activeServiceIDsForDate(ctx, queryDayMidnight)
	if err != nil {
		return nil, err
	}
	previousDay, err := api.activeServiceIDsForDate(ctx, queryDayMidnight.AddDate(0, 0, -1))
	if err != nil {
		logging.ForComponent(ctx, "http_server").Warn("trips-for-route: previous service day unavailable",
			"zone", location.String(), "error", err)
		previousDay = nil
	}

	return newServiceDateResolverFor(queryDayMidnight, currentTime.In(location), serviceIDsByDay{
		QueryDay:    queryDay,
		PreviousDay: previousDay,
	}), nil
}

func (api *RestAPI) activeTripsInBlocks(
	ctx context.Context,
	blockIDs []string,
	dayIndex int,
	routeDay serviceDay,
	routeZone string,
	serviceDatesByZone map[string]*serviceDateResolver,
	agencyLocations map[string]*time.Location,
	currentTime time.Time,
) (map[string]time.Time, error) {
	serviceDaysByZone, serviceIDs := zoneServiceDays(serviceDatesByZone)
	blockIDs = slices.Compact(slices.Sorted(slices.Values(blockIDs)))
	if len(blockIDs) == 0 || len(serviceIDs) == 0 {
		return nil, nil
	}

	nullBlockIDs := make([]sql.NullString, len(blockIDs))
	for i, blockID := range blockIDs {
		nullBlockIDs[i] = nulls.String(blockID)
	}
	candidates, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDs(ctx, gtfsdb.GetTripsByBlockIDsParams{
		BlockIds:   nullBlockIDs,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch trips in blocks: %w", err)
	}
	zoneByRoute, err := api.routeZones(ctx, candidates, agencyLocations)
	if err != nil {
		return nil, err
	}

	activeTrips := make(map[string]time.Time)
	tripsInBlock := tripsByBlock(candidates)
	var betweenTrips []string
	for _, blockID := range blockIDs {
		if tripID, serviceDayMidnight, found := activeTripInBlock(tripsInBlock[blockID], dayIndex, serviceDaysByZone, zoneByRoute); found {
			activeTrips[tripID] = serviceDayMidnight
			continue
		}
		betweenTrips = append(betweenTrips, blockID)
	}

	routeOfTrip := make(map[string]string, len(candidates))
	for _, trip := range candidates {
		routeOfTrip[trip.ID] = trip.RouteID
	}
	for _, blockID := range betweenTrips {
		if err := api.resolveBlockBetweenTrips(ctx, blockBetweenTrips{
			blockID:           blockID,
			dayIndex:          dayIndex,
			routeZone:         routeZone,
			blockTrips:        tripsInBlock[blockID],
			serviceDaysByZone: serviceDaysByZone,
			zoneByRoute:       zoneByRoute,
			routeOfTrip:       routeOfTrip,
			currentTime:       currentTime,
		}, activeTrips); err != nil {
			return nil, err
		}
	}
	return activeTrips, nil
}

type blockBetweenTrips struct {
	blockID           string
	dayIndex          int
	routeZone         string
	blockTrips        []gtfsdb.GetTripsByBlockIDsRow
	serviceDaysByZone map[string][]serviceDay
	zoneByRoute       map[string]string
	routeOfTrip       map[string]string
	currentTime       time.Time
}

// resolveBlockBetweenTrips picks the trip a block sits on when none of its trips is
// running at the request time, the way Java's BlockStatusServiceImpl.computeLocations
// keeps a block during a layover. The block's scheduled span is measured once per
// candidate agency zone, and a trip is only taken from the pass that ran on its own
// agency's clock, so another agency's offset can never make it look active.
func (api *RestAPI) resolveBlockBetweenTrips(
	ctx context.Context,
	block blockBetweenTrips,
	activeTrips map[string]time.Time,
) error {
	for _, zone := range blockCandidateZones(block.blockTrips, block.zoneByRoute, block.routeZone) {
		days := block.serviceDaysByZone[zone]
		if block.dayIndex >= len(days) {
			continue
		}
		day := days[block.dayIndex]
		if !zoneBlockCoversTime(block.blockTrips, zone, day, block.zoneByRoute) {
			continue
		}

		zoneTrips, zoneServiceDays, err := api.resolveTripsForRouteBlocks(ctx, []tripsForRouteServiceDay{{
			blockIDs:      []string{block.blockID},
			serviceIDs:    day.serviceIDs,
			sinceMidnight: time.Duration(day.sinceMidnightNs),
			midnight:      day.midnight,
		}}, block.currentTime)
		if err != nil {
			return err
		}

		resolved := false
		for _, tripID := range zoneTrips {
			if block.zoneByRoute[block.routeOfTrip[tripID]] != zone {
				continue
			}
			resolved = true
			if _, found := activeTrips[tripID]; !found {
				activeTrips[tripID] = zoneServiceDays[tripID]
			}
		}
		if resolved {
			return nil
		}
	}

	return nil
}

// zoneBlockCoversTime reports whether the trips this zone's agencies run on the block
// bracket the request time on that zone's clock. A block is only between trips for an
// agency once that agency's own run has started and has not finished; without this the
// span of an interlined trip from another zone would stand in for it.
func zoneBlockCoversTime(
	blockTrips []gtfsdb.GetTripsByBlockIDsRow,
	zone string,
	day serviceDay,
	zoneByRoute map[string]string,
) bool {
	var first, last int64
	found := false
	for _, trip := range blockTrips {
		if zoneByRoute[trip.RouteID] != zone {
			continue
		}
		if _, runs := day.services[trip.ServiceID]; !runs {
			continue
		}
		if !trip.MinArrivalTime.Valid || !trip.MaxDepartureTime.Valid {
			continue
		}
		if !found || trip.MinArrivalTime.Int64 < first {
			first = trip.MinArrivalTime.Int64
		}
		if !found || trip.MaxDepartureTime.Int64 > last {
			last = trip.MaxDepartureTime.Int64
		}
		found = true
	}

	return found && first <= day.sinceMidnightNs && day.sinceMidnightNs <= last
}

// blockCandidateZones lists the zones a block's trips belong to, the queried route's
// zone first so a single-agency block resolves on the first pass.
func blockCandidateZones(
	blockTrips []gtfsdb.GetTripsByBlockIDsRow,
	zoneByRoute map[string]string,
	routeZone string,
) []string {
	zones := []string{routeZone}
	seen := map[string]bool{routeZone: true}
	for _, trip := range blockTrips {
		zone := zoneByRoute[trip.RouteID]
		if zone == "" || seen[zone] {
			continue
		}
		seen[zone] = true
		zones = append(zones, zone)
	}
	return zones
}

// zoneServiceDays returns each zone's service days and every service ID active on any of them.
func zoneServiceDays(resolvers map[string]*serviceDateResolver) (map[string][]serviceDay, []string) {
	daysByZone := make(map[string][]serviceDay, len(resolvers))
	serviceIDs := make(map[string]struct{})
	for zone, resolver := range resolvers {
		daysByZone[zone] = resolver.ServiceDays()
		for _, day := range daysByZone[zone] {
			for id := range day.services {
				serviceIDs[id] = struct{}{}
			}
		}
	}
	return daysByZone, slices.Collect(maps.Keys(serviceIDs))
}

// routeZones maps the route of each trip to its agency's timezone name.
func (api *RestAPI) routeZones(
	ctx context.Context,
	trips []gtfsdb.GetTripsByBlockIDsRow,
	agencyLocations map[string]*time.Location,
) (map[string]string, error) {
	routeIDs := make(map[string]struct{}, len(trips))
	for _, trip := range trips {
		routeIDs[trip.RouteID] = struct{}{}
	}
	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, slices.Collect(maps.Keys(routeIDs)))
	if err != nil {
		return nil, fmt.Errorf("fetch routes of block trips: %w", err)
	}
	zoneByRoute := make(map[string]string, len(routes))
	for _, route := range routes {
		if location, ok := agencyLocations[route.AgencyID]; ok {
			zoneByRoute[route.ID] = location.String()
		}
	}
	return zoneByRoute, nil
}

// tripsByBlock groups trips by block ID, keeping their order within each block.
func tripsByBlock(trips []gtfsdb.GetTripsByBlockIDsRow) map[string][]gtfsdb.GetTripsByBlockIDsRow {
	byBlock := make(map[string][]gtfsdb.GetTripsByBlockIDsRow)
	for _, trip := range trips {
		byBlock[trip.BlockID.String] = append(byBlock[trip.BlockID.String], trip)
	}
	return byBlock
}

// activeTripInBlock returns the earliest trip in service on the dayIndex service
// day, testing each trip in its own agency's zone. blockTrips must be ordered by
// min_arrival_time.
func activeTripInBlock(
	blockTrips []gtfsdb.GetTripsByBlockIDsRow,
	dayIndex int,
	serviceDaysByZone map[string][]serviceDay,
	zoneByRoute map[string]string,
) (string, time.Time, bool) {
	for _, trip := range blockTrips {
		days := serviceDaysByZone[zoneByRoute[trip.RouteID]]
		if dayIndex >= len(days) {
			continue
		}
		day := days[dayIndex]
		_, serviceRuns := day.services[trip.ServiceID]
		inService := trip.MinArrivalTime.Valid && trip.MaxDepartureTime.Valid &&
			trip.MinArrivalTime.Int64 <= day.sinceMidnightNs &&
			trip.MaxDepartureTime.Int64 >= day.sinceMidnightNs
		if serviceRuns && inService {
			return trip.ID, day.midnight, true
		}
	}
	return "", time.Time{}, false
}

// routeBlocksAndTripsInService returns the route's blocks and null-block trips
// running within day's window.
func (api *RestAPI) routeBlocksAndTripsInService(ctx context.Context, routeID string, day serviceDay) ([]string, []string, error) {
	if len(day.serviceIDs) == 0 {
		return nil, nil, nil
	}
	reqLogger := logging.ForComponent(ctx, "http_server")
	queries := api.GtfsManager.GtfsDB.Queries
	windowStart := day.sinceMidnightNs - int64(runningLate)
	windowEnd := day.sinceMidnightNs + int64(runningEarly)

	var blockIDs []string
	indexIDs, err := queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
		RouteID:    routeID,
		ServiceIds: day.serviceIDs,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(indexIDs) > 0 {
		indexedBlocks, err := queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
			FromTime:         nulls.Int64(windowStart),
			ToTime:           nulls.Int64(windowEnd),
			RouteID:          routeID,
			IndexIds:         indexIDs,
			ActiveServiceIds: day.serviceIDs,
			RouteServiceIds:  day.serviceIDs,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, blockID := range indexedBlocks {
			if blockID.Valid {
				blockIDs = append(blockIDs, blockID.String)
			}
		}
	}

	layoverBlocks, err := queries.GetActiveLayoverBlockIDsForRoute(ctx, gtfsdb.GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     day.serviceIDs,
		TimeRangeStart: windowStart,
		TimeRangeEnd:   windowEnd,
	})
	if err != nil {
		reqLogger.Warn("trips-for-route: failed to fetch layover blocks", "route_id", routeID, "error", err)
	}
	blockIDs = append(blockIDs, layoverBlocks...)

	nullBlockTrips, err := queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
		RouteID:        routeID,
		ServiceIds:     day.serviceIDs,
		TimeRangeStart: nulls.Int64(windowStart),
		TimeRangeEnd:   nulls.Int64(windowEnd),
	})
	if err != nil {
		reqLogger.Warn("trips-for-route: failed to fetch null-block trips", "route_id", routeID, "error", err)
		return blockIDs, nil, nil
	}
	return blockIDs, nullBlockTrips, nil
}

// tripWindowOverlapsRange reports whether the trip's scheduled window
// (MinArrivalTime/MaxDepartureTime, ns since midnight) overlaps [start, end] —
// the same test the discovery queries apply to the previous day. Trips with no
// stop_times never overlap.
func tripWindowOverlapsRange(trip gtfsdb.Trip, start, end time.Duration) bool {
	if !trip.MinArrivalTime.Valid || !trip.MaxDepartureTime.Valid {
		return false
	}
	return trip.MinArrivalTime.Int64 <= end.Nanoseconds() &&
		trip.MaxDepartureTime.Int64 >= start.Nanoseconds()
}

// locationOrDefault returns agencyID's time zone, or fallback when the agency is unknown.
func locationOrDefault(locations map[string]*time.Location, agencyID string, fallback *time.Location) *time.Location {
	if location, ok := locations[agencyID]; ok {
		return location
	}
	return fallback
}

// tripReferenceParams bundles the inputs the trips-for-route reference block is
// built from, so the builder does not grow another positional parameter each
// time a reference kind is added.
type tripReferenceParams struct {
	IncludeTrip     bool
	Trips           []models.TripsForRouteListEntry
	Stops           []gtfsdb.Stop
	PreFetchedTrips []gtfsdb.Trip
	StopIDMap       map[string][]string
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
	references.Situations = api.situationReferences(ctx, params.Situations)
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
	reqLogger := logging.ForComponent(ctx, "http_server")
	if len(sets.missing) == 0 {
		return
	}

	missingIDs := make([]string, 0, len(sets.missing))
	for id := range sets.missing {
		missingIDs = append(missingIDs, id)
	}

	trips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, missingIDs)
	if err != nil {
		logging.LogError(reqLogger, "failed to fetch trips for references", err)
		return
	}

	sets.collectPreFetchedTrips(trips)
}

// fillRoutesAndAgencies loads every route the collected trips belong to, plus
// the agency owning each of those routes.
func (api *RestAPI) fillRoutesAndAgencies(ctx context.Context, sets *tripReferenceSets) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	routeIDs := make([]string, 0, len(sets.routes))
	for id := range sets.routes {
		routeIDs = append(routeIDs, id)
	}
	if len(routeIDs) == 0 {
		return
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
	if err != nil {
		logging.LogError(reqLogger, "failed to fetch routes for references", err)
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
	reqLogger := logging.ForComponent(ctx, "http_server")
	agency, err := api.GtfsManager.FindAgency(ctx, agencyID)
	if err != nil {
		reqLogger.Error("failed to fetch agency for references", "error", err, "agency", agencyID)
		return
	}
	if agency != nil {
		sets.agencies[agency.ID] = models.AgencyReferenceFromDatabase(agency)
	}
}

// tripReferenceList emits the collected trips in combined-ID form. A trip whose
// route was never resolved is skipped, since its agency is unknown.
func (s *tripReferenceSets) tripReferenceList(includeTrip bool) []models.Trip {
	var tripsRefList []models.Trip
	if !includeTrip {
		return tripsRefList
	}

	tripsRefList = make([]models.Trip, 0, len(s.trips))

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
func (api *RestAPI) resolveDuplicatedBaseTrip(ctx context.Context, dupTripID string) (string, gtfsdb.Trip, error) {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, dupTripID)
	if err == nil {
		return dupTripID, trip, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", gtfsdb.Trip{}, err
	}

	stripped := stripNumericSuffix(dupTripID)
	if stripped == dupTripID {
		return dupTripID, gtfsdb.Trip{}, nil
	}

	strippedTrip, strippedErr := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, stripped)
	if strippedErr != nil {
		if !errors.Is(strippedErr, sql.ErrNoRows) {
			return "", gtfsdb.Trip{}, strippedErr
		}
		return dupTripID, gtfsdb.Trip{}, nil
	}
	return stripped, strippedTrip, nil
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
