package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForLocationHandler returns active trips near a geographic location, specified by
// lat/lon coordinates with latSpan/lonSpan bounds, including real-time status and schedule data.
func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	parsedReq, fieldErrors, err := api.parseAndValidateRequest(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Uncapped: this stop set only narrows the candidate trips, and a cap here
	// silently drops trips the spec says should all be returned.
	stopsInBounds := api.GtfsManager.GetStopsInBounds(ctx, parsedReq.LocationParams, 0, true)
	stopIDs := extractStopIDs(stopsInBounds)
	candidateTripIDs, err := api.candidateTripIDsForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Candidate selection runs before any trip's agency is known, so a trip's
	// service day can't be resolved in its own agency's zone yet — resolve it in
	// every configured agency's zone instead, since a trip near midnight is only
	// in service under its own agency's day. Each entry's reported service date
	// is settled per agency once the entries are built.
	serviceDatesByZone, err := api.serviceDateResolversByZone(ctx, parsedReq.AgencyLocations, parsedReq.CurrentTime)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	activeTrips := api.getActiveTrips(candidateTripIDs, api.GtfsManager.GetRealTimeVehicles())

	bounds := internalgtfs.BoundsFromParams(parsedReq.LocationParams, true)
	visibleTripIDs := make([]string, 0, len(activeTrips))
	positionedTripIDs := make(map[string]struct{}, len(activeTrips))
	for _, vehicle := range activeTrips {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		if vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
			continue
		}
		positionedTripIDs[vehicle.Trip.ID.ID] = struct{}{}
		vLat, vLon := float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude)
		if utils.BoundsContain(bounds, vLat, vLon) {
			visibleTripIDs = append(visibleTripIDs, vehicle.Trip.ID.ID)
		}
	}

	scheduledTripIDSet := make(map[string]struct{})
	for _, serviceDates := range serviceDatesByZone {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}
		zoneTripIDs, err := api.scheduledTripIDsInBounds(ctx, stopIDs, bounds, serviceDates, parsedReq.CurrentTime, positionedTripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		for _, tripID := range zoneTripIDs {
			scheduledTripIDSet[tripID] = struct{}{}
		}
	}
	for tripID := range scheduledTripIDSet {
		visibleTripIDs = append(visibleTripIDs, tripID)
	}

	trips, err := queryInBatches(ctx, visibleTripIDs, api.GtfsManager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routeIDs := make([]string, 0, len(trips))
	tripRouteMap := make(map[string]string)
	for _, trip := range trips {
		routeIDs = append(routeIDs, trip.RouteID)
		tripRouteMap[trip.ID] = trip.RouteID
	}

	var routes []gtfsdb.Route
	if len(routeIDs) > 0 {
		routes, err = queryInBatches(ctx, routeIDs, api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	tripAgencyMap := make(map[string]string)
	routeAgencyMap := make(map[string]string)

	for _, route := range routes {
		routeAgencyMap[route.ID] = route.AgencyID
	}
	for tripID, routeID := range tripRouteMap {
		if agencyID, ok := routeAgencyMap[routeID]; ok {
			tripAgencyMap[tripID] = agencyID
		}
	}

	// Build entries from pre-fetched trip data
	result, situations := api.buildTripsForLocationEntries(ctx, trips, tripAgencyMap, routeAgencyMap, parsedReq, w, r)
	if result == nil {
		return
	}

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	references := *models.NewEmptyReferences()

	includeReferences := ShouldIncludeReferences(r)

	if includeReferences {
		referencedStops, stopIDsByBareID, stopsErr := api.stopsReferencedByEntries(ctx, result)
		if stopsErr != nil {
			api.serverErrorResponse(w, r, stopsErr)
			return
		}

		references = api.BuildReference(w, r, ctx, ReferenceParams{
			IncludeTrip:     parsedReq.IncludeTrip,
			Stops:           referencedStops,
			StopIDsByBareID: stopIDsByBareID,
			Trips:           result,
			Situations:      situations,
		})
	}

	// The search clamped its bounds, so outOfRange has to be reported against
	// the clamped bounds too.
	outOfRange := api.GtfsManager.CheckIfOutOfBounds(parsedReq.LocationParams, true)
	response := models.NewListResponseWithRange(result, references, outOfRange, api.Clock, false)
	api.sendResponse(w, r, response)
}

// tripsForLocationRequest holds the parsed and validated query parameters for
// the trips-for-location endpoint.
type tripsForLocationRequest struct {
	LocationParams  *internalgtfs.LocationParams
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	CurrentTime     time.Time
	AgencyLocations map[string]*time.Location
}

func (api *RestAPI) parseAndValidateRequest(r *http.Request) (*tripsForLocationRequest, map[string][]string, error) {
	loc, fieldErrors := api.parseLocationParams(r, nil)

	queryParams := r.URL.Query()

	includeTrip := parseIncludeTrip(queryParams)
	includeSchedule, _ := strconv.ParseBool(queryParams.Get("includeSchedule"))
	// Intentionally defaulting includeStatus to false to align with includeSchedule
	// behavior for this endpoint, even though trips-for-route defaults to true.
	includeStatus, _ := strconv.ParseBool(queryParams.Get("includeStatus"))

	agencies, agenciesErr := api.GtfsManager.GetAgencies(r.Context())

	if agenciesErr != nil {
		return nil, nil, agenciesErr
	}

	if len(agencies) == 0 {
		return nil, nil, errors.New("no agencies configured in GTFS manager")
	}

	agencyLocations := make(map[string]*time.Location, len(agencies))
	for _, agency := range agencies {
		location, locationErr := loadAgencyLocation(agency.ID, agency.Timezone)
		if locationErr != nil {
			return nil, nil, locationErr
		}
		agencyLocations[agency.ID] = location
	}
	currentLocation := agencyLocations[agencies[0].ID]

	currentTime, timeFieldErrors := api.resolveCurrentTime(queryParams.Get("time"), currentLocation)
	fieldErrors = mergeFieldErrors(fieldErrors, timeFieldErrors)

	if len(fieldErrors) > 0 {
		return nil, fieldErrors, nil
	}

	parsedReq := &tripsForLocationRequest{
		LocationParams:  loc,
		IncludeTrip:     includeTrip,
		IncludeSchedule: includeSchedule,
		IncludeStatus:   includeStatus,
		CurrentTime:     currentTime,
		AgencyLocations: agencyLocations,
	}
	return parsedReq, nil, nil
}

// parseIncludeTrip parses the includeTrip query parameter, defaulting to true when omitted
// and to false when present but not a valid boolean.
func parseIncludeTrip(queryParams url.Values) bool {
	if !queryParams.Has("includeTrip") {
		return true
	}
	includeTrip, _ := strconv.ParseBool(queryParams.Get("includeTrip"))
	return includeTrip
}

// resolveCurrentTime resolves the query time: the explicit time parameter if supplied,
// otherwise the current server clock.
func (api *RestAPI) resolveCurrentTime(timeParam string, currentLocation *time.Location) (time.Time, map[string][]string) {
	if timeParam == "" {
		return api.Clock.Now().In(currentLocation), nil
	}
	_, currentTime, timeFieldErrors, _ := utils.ParseTimeParameter(timeParam, currentLocation, api.Clock)
	return currentTime, timeFieldErrors
}

// mergeFieldErrors appends src's entries onto dst, allocating dst if necessary.
func mergeFieldErrors(dst, src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string][]string)
	}
	for k, v := range src {
		dst[k] = append(dst[k], v...)
	}
	return dst
}

// scheduledTripIDsInBounds returns trips that serve an in-bounds stop, are in
// service at currentTime, and whose schedule-derived position falls inside the
// search box.
//
// A live vehicle's reported position is the better signal when one exists, so
// a trip in positionedTripIDs is skipped here rather than being placed twice.
// A trip with a live vehicle but no reported position is not in
// positionedTripIDs and still runs through the schedule below.
func (api *RestAPI) scheduledTripIDsInBounds(
	ctx context.Context,
	stopIDs []string,
	bounds utils.CoordinateBounds,
	serviceDates *serviceDateResolver,
	currentTime time.Time,
	positionedTripIDs map[string]struct{},
) ([]string, error) {
	if len(stopIDs) == 0 {
		return nil, nil
	}

	// Blocked trips are found separately, anchored on the block's whole span
	// rather than any one trip's own — see blockedScheduledTripIDsInBounds.
	blockVisible, err := api.blockedScheduledTripIDsInBounds(ctx, stopIDs, bounds, serviceDates, currentTime, positionedTripIDs)
	if err != nil {
		return nil, err
	}

	blocklessVisible, err := api.blocklessScheduledTripIDsInBounds(ctx, stopIDs, bounds, serviceDates, currentTime, positionedTripIDs)
	if err != nil {
		return nil, err
	}

	return append(blockVisible, blocklessVisible...), nil
}

// blocklessScheduledTripIDsInBounds places the trips that have no block_id by
// projecting each one's own schedule onto its own shape. Per-trip containment
// is the right question for them precisely because a blockless trip is its own
// block, so there is no layover to miss.
func (api *RestAPI) blocklessScheduledTripIDsInBounds(
	ctx context.Context,
	stopIDs []string,
	bounds utils.CoordinateBounds,
	serviceDates *serviceDateResolver,
	currentTime time.Time,
	positionedTripIDs map[string]struct{},
) ([]string, error) {
	candidateIDs, err := api.inServiceTripIDs(ctx, stopIDs, serviceDates, positionedTripIDs)
	if err != nil || len(candidateIDs) == 0 {
		return nil, err
	}

	trips, err := queryInBatches(ctx, candidateIDs, api.GtfsManager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		return nil, err
	}

	stopTimesByTrip, err := api.stopTimesByTrip(ctx, candidateIDs)
	if err != nil {
		return nil, err
	}
	shapesByID, err := api.shapePointsForTrips(ctx, trips)
	if err != nil {
		return nil, err
	}
	// One lookup for the union of every candidate's stops. Fetching per trip
	// would be a query per candidate, and the candidate set runs to hundreds.
	stopsByID, err := api.fetchStopCoordsForStopTimes(ctx, unionStopTimes(stopTimesByTrip))
	if err != nil {
		return nil, err
	}

	var visible []string
	for _, trip := range trips {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !trip.ShapeID.Valid {
			continue
		}

		position := api.scheduledPositionAtTime(
			stopTimesByTrip[trip.ID],
			shapesByID[trip.ShapeID.String],
			stopsByID,
			currentTime,
			serviceDates.Resolve(trip),
		)
		if position != nil && utils.BoundsContain(bounds, position.Lat, position.Lon) {
			visible = append(visible, trip.ID)
		}
	}
	return visible, nil
}

// unionStopTimes flattens per-trip stop times into one slice, so their stops can
// be fetched in a single query.
func unionStopTimes(stopTimesByTrip map[string][]gtfsdb.StopTime) []gtfsdb.StopTime {
	total := 0
	for _, stopTimes := range stopTimesByTrip {
		total += len(stopTimes)
	}

	union := make([]gtfsdb.StopTime, 0, total)
	for _, stopTimes := range stopTimesByTrip {
		union = append(union, stopTimes...)
	}
	return union
}

// inServiceTripIDs collects trips serving an in-bounds stop whose scheduled
// span overlaps the running window [sinceMidnight-runningLate,
// sinceMidnight+runningEarly], across the query day and the day before it —
// the same grace window trips-for-route's runsOn applies, so a trip that just
// ended or is about to start is still offered as a candidate here too.
func (api *RestAPI) inServiceTripIDs(
	ctx context.Context,
	stopIDs []string,
	serviceDates *serviceDateResolver,
	positionedTripIDs map[string]struct{},
) ([]string, error) {
	seen := make(map[string]struct{})
	var candidateIDs []string

	for _, day := range serviceDates.ServiceDays() {
		if len(day.serviceIDs) == 0 {
			continue
		}

		windowStart := day.sinceMidnightNs - int64(runningLate)
		windowEnd := day.sinceMidnightNs + int64(runningEarly)
		// Reserve room for the two window scalars and the ServiceIds slice this
		// statement also binds — queryInBatches alone would size the StopIds
		// batch as if it were the only bind, and a day with enough active
		// service IDs could still push the statement over the limit.
		reserved := len(day.serviceIDs) + 2
		tripIDs, err := queryInBatchesReserving(ctx, stopIDs, reserved, func(ctx context.Context, batch []string) ([]string, error) {
			return api.GtfsManager.GtfsDB.Queries.GetInServiceTripIDsForStops(ctx, gtfsdb.GetInServiceTripIDsForStopsParams{
				StopIds:     batch,
				ServiceIds:  day.serviceIDs,
				WindowStart: nulls.Int64(windowStart),
				WindowEnd:   nulls.Int64(windowEnd),
			})
		})
		if err != nil {
			return nil, err
		}

		for _, tripID := range tripIDs {
			if _, positioned := positionedTripIDs[tripID]; positioned {
				continue
			}
			if _, ok := seen[tripID]; ok {
				continue
			}
			seen[tripID] = struct{}{}
			candidateIDs = append(candidateIDs, tripID)
		}
	}
	return candidateIDs, nil
}

// blockAnchor is one block's chosen anchor trip for a given running window.
type blockAnchor struct {
	blockID string
	tripID  string
}

// selectBlockAnchors groups spans by block ID and returns one anchor per block
// whose combined span — [min(min_arrival_time), max(max_departure_time)]
// across every trip in the block — overlaps [windowStart, windowEnd]. This is
// what makes a bus in a scheduled layover between two block trips still
// count as active: the layover falls inside the block's combined span even
// though it falls inside neither individual trip's own span.
//
// spans must already be sorted by block ID, then by min_arrival_time
// ascending — GetTripSpansForBlocks orders its rows this way. The anchor is
// the last trip in the block with min_arrival_time <= windowEnd, i.e. the
// trip currently running or most recently run, so computeScheduledBlockSnapshot
// resolves the shift that's active now rather than an earlier shift sharing
// the same block ID. Falls back to the block's first trip when none has
// started yet.
func selectBlockAnchors(spans []gtfsdb.GetTripSpansForBlocksRow, windowStart, windowEnd int64) []blockAnchor {
	var anchors []blockAnchor

	for i := 0; i < len(spans); {
		j := i
		for j < len(spans) && spans[j].BlockID == spans[i].BlockID {
			j++
		}
		group := spans[i:j]
		i = j

		if anchor, ok := selectAnchorInGroup(group, windowStart, windowEnd); ok {
			anchors = append(anchors, anchor)
		}
	}

	return anchors
}

// selectAnchorInGroup picks the anchor for a single block's trips, already
// sorted by min_arrival_time ascending.
func selectAnchorInGroup(group []gtfsdb.GetTripSpansForBlocksRow, windowStart, windowEnd int64) (blockAnchor, bool) {
	if len(group) == 0 || !group[0].MinArrivalTime.Valid || !group[0].MaxDepartureTime.Valid {
		return blockAnchor{}, false
	}

	spanStart := group[0].MinArrivalTime.Int64
	spanEnd := group[0].MaxDepartureTime.Int64
	anchor := group[0]
	for _, row := range group {
		if !row.MinArrivalTime.Valid || !row.MaxDepartureTime.Valid {
			continue
		}
		if row.MaxDepartureTime.Int64 > spanEnd {
			spanEnd = row.MaxDepartureTime.Int64
		}
		if row.MinArrivalTime.Int64 <= windowEnd {
			anchor = row
		}
	}

	if spanStart > windowEnd || spanEnd < windowStart {
		return blockAnchor{}, false
	}
	return blockAnchor{blockID: anchor.BlockID.String, tripID: anchor.ID}, true
}

// blockedScheduledTripIDsInBounds returns, per service day, the in-bounds
// active trip of every block whose combined span overlaps that day's running
// window and serves one of stopIDs. Position comes from
// computeScheduledBlockSnapshot — the same block/shift interpolation
// BuildTripStatus already uses for entries — so a bus in a scheduled layover
// between two block trips is still found. inServiceTripIDs cannot see this
// case: a layover falls inside neither trip's own scheduled span, only the
// block's combined one.
func (api *RestAPI) blockedScheduledTripIDsInBounds(
	ctx context.Context,
	stopIDs []string,
	bounds utils.CoordinateBounds,
	serviceDates *serviceDateResolver,
	currentTime time.Time,
	positionedTripIDs map[string]struct{},
) ([]string, error) {
	// One cache across every anchor snapshot computed below, so two anchors
	// landing in the same shift (e.g. the same block found on both the query
	// day and the previous day near midnight) share one computation.
	scan := &blockCandidateScan{
		bounds:            bounds,
		currentTime:       currentTime,
		positionedTripIDs: positionedTripIDs,
		seenActiveTrip:    make(map[string]struct{}),
	}
	ctx = WithSnapshotCache(ctx, newSnapshotCache())
	serviceDateForDay := []time.Time{serviceDates.queryDayMidnight, serviceDates.queryDayMidnight.AddDate(0, 0, -1)}

	var visible []string
	for dayIndex, day := range serviceDates.ServiceDays() {
		if len(day.serviceIDs) == 0 {
			continue
		}
		dayVisible, err := api.blockedTripIDsForServiceDay(ctx, stopIDs, day, serviceDateForDay[dayIndex], scan)
		if err != nil {
			return nil, err
		}
		visible = append(visible, dayVisible...)
	}

	return visible, nil
}

// blockCandidateScan carries the state one block scan shares across both
// service days: what counts as in bounds, and which trips are already spoken
// for by a live vehicle or an earlier day's anchor.
type blockCandidateScan struct {
	bounds            utils.CoordinateBounds
	currentTime       time.Time
	positionedTripIDs map[string]struct{}
	seenActiveTrip    map[string]struct{}
}

// blockedTripIDsForServiceDay resolves one service day's blocks to the active
// trips that fall inside the search box, recording each one in scan so a block
// found on both days is only emitted once.
func (api *RestAPI) blockedTripIDsForServiceDay(
	ctx context.Context,
	stopIDs []string,
	day serviceDay,
	serviceDate time.Time,
	scan *blockCandidateScan,
) ([]string, error) {
	spans, err := api.tripSpansForBlocksServingStops(ctx, stopIDs, day)
	if err != nil || len(spans) == 0 {
		return nil, err
	}

	windowStart := day.sinceMidnightNs - int64(runningLate)
	windowEnd := day.sinceMidnightNs + int64(runningEarly)

	var visible []string
	for _, anchor := range selectBlockAnchors(spans, windowStart, windowEnd) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		tripID, ok := api.activeTripInBoundsForAnchor(ctx, anchor.tripID, serviceDate, scan)
		if !ok {
			continue
		}
		visible = append(visible, tripID)
	}
	return visible, nil
}

// tripSpansForBlocksServingStops returns every trip span belonging to a block
// that serves one of stopIDs on this service day. Time is not filtered here:
// the window applies to a block's combined span, which the caller tests.
func (api *RestAPI) tripSpansForBlocksServingStops(
	ctx context.Context,
	stopIDs []string,
	day serviceDay,
) ([]gtfsdb.GetTripSpansForBlocksRow, error) {
	blockIDs, err := queryInBatchesReserving(ctx, stopIDs, len(day.serviceIDs),
		func(ctx context.Context, batch []string) ([]sql.NullString, error) {
			return api.GtfsManager.GtfsDB.Queries.GetBlockIDsForStops(ctx, gtfsdb.GetBlockIDsForStopsParams{
				StopIds:    batch,
				ServiceIds: day.serviceIDs,
			})
		})
	if err != nil || len(blockIDs) == 0 {
		return nil, err
	}

	blockIDStrings := make([]string, len(blockIDs))
	for i, blockID := range blockIDs {
		blockIDStrings[i] = blockID.String
	}

	return queryInBatchesReserving(ctx, blockIDStrings, len(day.serviceIDs),
		func(ctx context.Context, batch []string) ([]gtfsdb.GetTripSpansForBlocksRow, error) {
			nullableBatch := make([]sql.NullString, len(batch))
			for i, blockID := range batch {
				nullableBatch[i] = nulls.String(blockID)
			}
			return api.GtfsManager.GtfsDB.Queries.GetTripSpansForBlocks(ctx, gtfsdb.GetTripSpansForBlocksParams{
				BlockIds:   nullableBatch,
				ServiceIds: day.serviceIDs,
			})
		})
}

// activeTripInBoundsForAnchor interpolates the anchor's block and reports the
// shift's active trip when it is running now, has not already been emitted,
// is not already placed by a live vehicle, and sits inside the search box.
func (api *RestAPI) activeTripInBoundsForAnchor(
	ctx context.Context,
	anchorTripID string,
	serviceDate time.Time,
	scan *blockCandidateScan,
) (string, bool) {
	snap := api.computeScheduledBlockSnapshot(ctx, anchorTripID, scan.currentTime, serviceDate)
	if snap == nil || snap.ActiveTripID == "" || !snap.InRange {
		return "", false
	}
	if _, seen := scan.seenActiveTrip[snap.ActiveTripID]; seen {
		return "", false
	}
	scan.seenActiveTrip[snap.ActiveTripID] = struct{}{}
	if _, positioned := scan.positionedTripIDs[snap.ActiveTripID]; positioned {
		return "", false
	}

	pos, _ := positionAndOrientationAtDistance(
		snap.ActiveTripShape, snap.ActiveTripCumulativeDistances, snap.ActiveTripScheduledDistance)
	if pos == nil || !utils.BoundsContain(scan.bounds, pos.Lat, pos.Lon) {
		return "", false
	}
	return snap.ActiveTripID, true
}

func (api *RestAPI) stopTimesByTrip(ctx context.Context, tripIDs []string) (map[string][]gtfsdb.StopTime, error) {
	stopTimes, err := queryInBatches(ctx, tripIDs, api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs)
	if err != nil {
		return nil, err
	}

	byTrip := make(map[string][]gtfsdb.StopTime, len(tripIDs))
	for _, stopTime := range stopTimes {
		byTrip[stopTime.TripID] = append(byTrip[stopTime.TripID], stopTime)
	}
	return byTrip, nil
}

func (api *RestAPI) shapePointsForTrips(ctx context.Context, trips []gtfsdb.Trip) (map[string][]gtfs.ShapePoint, error) {
	shapeIDs := make([]string, 0, len(trips))
	seen := make(map[string]struct{}, len(trips))
	for _, trip := range trips {
		if !trip.ShapeID.Valid {
			continue
		}
		if _, ok := seen[trip.ShapeID.String]; ok {
			continue
		}
		seen[trip.ShapeID.String] = struct{}{}
		shapeIDs = append(shapeIDs, trip.ShapeID.String)
	}

	byID := make(map[string][]gtfs.ShapePoint, len(shapeIDs))
	if len(shapeIDs) == 0 {
		return byID, nil
	}

	shapePoints, err := queryInBatches(ctx, shapeIDs, api.GtfsManager.GtfsDB.Queries.GetShapePointsByIDs)
	if err != nil {
		return nil, err
	}
	for _, point := range shapePoints {
		byID[point.ShapeID] = append(byID[point.ShapeID], gtfs.ShapePoint{
			Latitude:  point.Lat,
			Longitude: point.Lon,
		})
	}
	return byID, nil
}

// stopsReferencedByEntries fetches the stops the response actually refers to:
// those on each entry's schedule, plus the closest and next stops on its status.
// The in-bounds stop set is deliberately not included — it is a candidate-trip
// selection detail, and stops on it that no returned trip serves have nothing in
// the response pointing at them.
func (api *RestAPI) stopsReferencedByEntries(ctx context.Context, entries []models.TripsForLocationListEntry) ([]gtfsdb.Stop, map[string]string, error) {
	stopIDsByBareID := make(map[string]string)

	for _, entry := range entries {
		collectStopIDsFromSchedule(entry.Schedule, stopIDsByBareID)
		if entry.Status == nil {
			continue
		}
		for _, combinedID := range []string{entry.Status.ClosestStop, entry.Status.NextStop} {
			_, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedID)
			if err != nil {
				continue
			}
			if _, exists := stopIDsByBareID[bareID]; !exists {
				stopIDsByBareID[bareID] = combinedID
			}
		}
	}

	if len(stopIDsByBareID) == 0 {
		return nil, nil, nil
	}

	bareIDs := make([]string, 0, len(stopIDsByBareID))
	for bareID := range stopIDsByBareID {
		bareIDs = append(bareIDs, bareID)
	}

	stops, err := queryInBatches(ctx, bareIDs, api.GtfsManager.GtfsDB.Queries.GetStopsByIDs)
	return stops, stopIDsByBareID, err
}

// candidateTripIDsForStops returns the IDs of the trips serving any of these
// stops. IDs may repeat across batches; the caller sets them.
func (api *RestAPI) candidateTripIDsForStops(ctx context.Context, stopIDs []string) ([]string, error) {
	return queryInBatches(ctx, stopIDs, api.GtfsManager.GtfsDB.Queries.GetTripIDsForStops)
}

func extractStopIDs(stops []gtfsdb.Stop) []string {
	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.ID
	}
	return stopIDs
}

func (api *RestAPI) getActiveTrips(candidateTripIDs []string, realTimeVehicles []gtfs.Vehicle) map[string]gtfs.Vehicle {
	trips := make(map[string]bool, len(candidateTripIDs))
	for _, tripID := range candidateTripIDs {
		trips[tripID] = true
	}
	activeTrips := make(map[string]gtfs.Vehicle)
	for _, vehicle := range realTimeVehicles {
		if vehicle.Trip != nil && trips[vehicle.Trip.ID.ID] {
			activeTrips[vehicle.Trip.ID.ID] = vehicle
		}
	}
	return activeTrips
}

// buildTripsForLocationEntries builds trip entries from pre-fetched batch data,
// returning the entries alongside the situations they reference.
// It returns nil entries only when an error response has already been sent to
// the client.
func (api *RestAPI) buildTripsForLocationEntries(
	ctx context.Context,
	trips []gtfsdb.Trip,
	tripAgencyMap map[string]string,
	routeAgencyMap map[string]string,
	request *tripsForLocationRequest,
	w http.ResponseWriter,
	r *http.Request,
) ([]models.TripsForLocationListEntry, []situationRef) {
	if len(trips) == 0 {
		return []models.TripsForLocationListEntry{}, nil
	}

	tripsMap := make(map[string]gtfsdb.Trip)
	blockIDsByAgency := make(map[string]map[string]struct{})
	agencyIDs := make(map[string]struct{})
	var validVehicleTrips []string
	var shapedTrips []gtfsdb.Trip

	for _, trip := range trips {
		// Ensure we only process trips that have a valid agency mapping
		agencyID, ok := tripAgencyMap[trip.ID]
		if !ok {
			continue
		}
		validVehicleTrips = append(validVehicleTrips, trip.ID)
		tripsMap[trip.ID] = trip
		shapedTrips = append(shapedTrips, trip)

		// A trip whose agency has no resolvable timezone is skipped later when
		// entries are built, so its block and service-day data is never needed.
		if _, hasLocation := request.AgencyLocations[agencyID]; !hasLocation {
			continue
		}
		agencyIDs[agencyID] = struct{}{}
		if trip.BlockID.Valid {
			agencyBlockIDs := blockIDsByAgency[agencyID]
			if agencyBlockIDs == nil {
				agencyBlockIDs = make(map[string]struct{})
				blockIDsByAgency[agencyID] = agencyBlockIDs
			}
			agencyBlockIDs[trip.BlockID.String] = struct{}{}
		}
	}

	shapesMap, err := api.shapePointsForTrips(ctx, shapedTrips)
	if err != nil {
		api.Logger.Warn("failed to bulk fetch shapes", "error", err)
		shapesMap = map[string][]gtfs.ShapePoint{}
	}

	// Every entry needs its agency's resolver regardless of includeSchedule, and
	// the block lookup below needs that same agency's query-day service IDs —
	// fetched once per agency here rather than the block lookup querying it
	// again itself.
	services := make(map[string]serviceIDsByDay, len(agencyIDs))
	serviceDatesByAgency := make(map[string]*serviceDateResolver, len(agencyIDs))
	for agencyID := range agencyIDs {
		agencyLocation := request.AgencyLocations[agencyID]
		queryDayMidnight := serviceDateMidnight(request.CurrentTime, agencyLocation)
		days, err := api.serviceIDsForDays(ctx, queryDayMidnight)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return nil, nil
		}
		services[agencyID] = days
		serviceDatesByAgency[agencyID] = newServiceDateResolverFor(
			queryDayMidnight, request.CurrentTime.In(agencyLocation), days)
	}

	stopTimesMap := make(map[string][]gtfsdb.StopTime)
	blockTripsMap := make(map[blockTripsKey][]gtfsdb.GetTripsByBlockIDsRow)
	var allStopIDs []string

	if request.IncludeSchedule {
		stopTimesRaw, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, validVehicleTrips)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return nil, nil
		}
		for _, st := range stopTimesRaw {
			stopTimesMap[st.TripID] = append(stopTimesMap[st.TripID], st)
			allStopIDs = append(allStopIDs, st.StopID)
		}

		for agencyID, agencyBlockIDs := range blockIDsByAgency {
			blockIDsNull := make([]sql.NullString, 0, len(agencyBlockIDs))
			for id := range agencyBlockIDs {
				blockIDsNull = append(blockIDsNull, nulls.String(id))
			}

			params := gtfsdb.GetTripsByBlockIDsParams{
				BlockIds:   blockIDsNull,
				ServiceIds: services[agencyID].QueryDay,
			}

			blockTripsRaw, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDs(ctx, params)
			if err == nil {
				missingRouteIDs := make([]string, 0)
				for _, bt := range blockTripsRaw {
					if _, found := routeAgencyMap[bt.RouteID]; !found {
						missingRouteIDs = append(missingRouteIDs, bt.RouteID)
					}
				}
				if len(missingRouteIDs) > 0 {
					routes, routeErr := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, missingRouteIDs)
					if routeErr != nil {
						api.Logger.Warn("failed to fetch block trip routes", "agency_id", agencyID, "error", routeErr)
						continue
					}
					for _, route := range routes {
						routeAgencyMap[route.ID] = route.AgencyID
					}
				}
				for _, bt := range blockTripsRaw {
					if bt.BlockID.Valid && routeAgencyMap[bt.RouteID] == agencyID {
						key := blockTripsKey{agencyID: agencyID, blockID: bt.BlockID.String}
						blockTripsMap[key] = append(blockTripsMap[key], bt)
					}
				}
			} else {
				api.Logger.Warn("failed to bulk fetch block trips", "agency_id", agencyID, "error", err)
			}
		}
	}

	stopCoords := make(map[string]struct{ lat, lon float64 })
	if len(allStopIDs) > 0 {
		stopsRaw, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, allStopIDs)
		if err == nil {
			for _, s := range stopsRaw {
				stopCoords[s.ID] = struct{ lat, lon float64 }{lat: s.Lat, lon: s.Lon}
			}
		} else {
			api.Logger.Warn("failed to bulk fetch stops", "error", err, "stop_count", len(allStopIDs))
		}
	}

	// Batch-fetch frequencies; success seeds nil to skip fallback queries.
	freqMap := make(map[string][]gtfsdb.Frequency)
	if len(validVehicleTrips) > 0 {
		var freqErr error
		freqMap, freqErr = api.fetchFrequenciesForTrips(ctx, validVehicleTrips)
		if freqErr != nil {
			api.serverErrorResponse(w, r, freqErr)
			return nil, nil
		}
	}

	var result []models.TripsForLocationListEntry
	situations := newSituationCollector()

	for _, tripID := range validVehicleTrips {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return nil, nil
		}

		agencyID := tripAgencyMap[tripID]
		tripData, tripFound := tripsMap[tripID]
		if !tripFound {
			continue
		}
		agencyLocation, locationFound := request.AgencyLocations[agencyID]
		if !locationFound {
			api.Logger.Warn("missing timezone for trip agency", "trip_id", tripID, "agency_id", agencyID)
			continue
		}
		serviceDate := serviceDatesByAgency[agencyID].Resolve(tripData)
		frequency, freqErr := api.frequencyForEntry(ctx, freqMap, tripID, serviceDate, request.CurrentTime)
		if freqErr != nil {
			api.serverErrorResponse(w, r, freqErr)
			return nil, nil
		}

		var schedule *models.TripsSchedule
		var status *models.TripStatus

		if request.IncludeSchedule {
			var shapePoints []gtfs.ShapePoint
			if tripData.ShapeID.Valid {
				shapePoints = shapesMap[tripData.ShapeID.String]
			}

			var blockTrips []gtfsdb.GetTripsByBlockIDsRow
			if tripData.BlockID.Valid {
				blockTrips = blockTripsMap[blockTripsKey{agencyID: agencyID, blockID: tripData.BlockID.String}]
			}

			schedule = api.buildScheduleFromMemory(scheduleData{
				trip:            tripData,
				agencyID:        agencyID,
				currentLocation: agencyLocation,
				stopTimes:       stopTimesMap[tripID],
				shapePoints:     shapePoints,
				stopCoords:      stopCoords,
				blockTrips:      blockTrips,
			}, frequency)
		}

		if request.IncludeStatus {
			var statusErr error
			status, _, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, serviceDate, request.CurrentTime, freqMap)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "tripID", tripID, "error", statusErr)
				status = nil
			}
		}

		// The trip's route and agency are already resolved here, so the alerts
		// are looked up directly rather than through GetSituationIDsForTrip,
		// which would re-query both per trip and discard the alerts we need
		// for the situation references.
		alerts := api.GtfsManager.GetAlertsByIDs(tripID, tripData.RouteID, agencyID)

		entry := models.TripsForLocationListEntry{
			Frequency:    frequency,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  serviceDate.UnixMilli(),
			SituationIds: situations.add(alerts, agencyID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}
	return result, situations.refs
}

type blockTripsKey struct {
	agencyID string
	blockID  string
}

// serviceDateMidnight returns the start of the service day in an agency's timezone.
func serviceDateMidnight(currentTime time.Time, agencyLocation *time.Location) time.Time {
	_, midnight := utils.ServiceDateMidnight(nil, currentTime.In(agencyLocation))
	return midnight
}

// serviceDateResolversByZone builds one serviceDateResolver per distinct agency
// time zone in locations. Candidate selection runs before any trip's agency is
// known, so a candidate can't be resolved in its own agency's zone directly —
// resolving in every configured zone instead means a trip near midnight is
// still found under whichever zone actually has it in service. Almost every
// deployment has a single zone, so this is one resolver in the common case.
func (api *RestAPI) serviceDateResolversByZone(
	ctx context.Context,
	locations map[string]*time.Location,
	currentTime time.Time,
) (map[string]*serviceDateResolver, error) {
	resolvers := make(map[string]*serviceDateResolver, len(locations))
	for _, location := range locations {
		zoneName := location.String()
		if _, ok := resolvers[zoneName]; ok {
			continue
		}
		queryDayMidnight := serviceDateMidnight(currentTime, location)
		days, err := api.serviceIDsForDays(ctx, queryDayMidnight)
		if err != nil {
			return nil, err
		}
		resolvers[zoneName] = newServiceDateResolverFor(queryDayMidnight, currentTime.In(location), days)
	}
	return resolvers, nil
}

func (api *RestAPI) buildScheduleForTrip(
	ctx context.Context,
	tripID, agencyID string, serviceDate time.Time,
	currentLocation *time.Location,
	freqMap map[string][]gtfsdb.Frequency,
) (*models.TripsSchedule, error) {
	shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	var shapePoints []gtfs.ShapePoint
	if len(shapeRows) > 1 {
		shapePoints = shapeRowsToPoints(shapeRows)
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	nextTripID, previousTripID, stopTimes, err := api.GetNextAndPreviousTripIDs(ctx, &trip, agencyID, serviceDate)
	if err != nil {
		return nil, err
	}

	stopTimesList := buildStopTimesList(api, ctx, stopTimes, shapePoints, agencyID)

	frequency, freqErr := api.frequencyForEntry(ctx, freqMap, tripID, serviceDate, serviceDate)
	if freqErr != nil {
		return nil, freqErr
	}
	return &models.TripsSchedule{
		Frequency:      frequency,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}, nil
}

func buildStopTimesList(api *RestAPI, ctx context.Context, stopTimes []gtfsdb.StopTime, shapePoints []gtfs.ShapePoint, agencyID string) []models.StopTime {

	// Batch-fetch all stop coordinates at once
	stopIDs := make([]string, len(stopTimes))
	for i, st := range stopTimes {
		stopIDs[i] = st.StopID
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)

	// Create a map for quick stop coordinate lookup
	stopCoords := make(map[string]struct{ lat, lon float64 })
	if err != nil {
		// Log the error but continue - distances will be 0 for all stops
		api.Logger.Warn("Failed to batch-fetch stop coordinates for distance calculation",
			"error", err,
			"agency_id", agencyID,
			"stop_count", len(stopIDs))
	} else {
		for _, stop := range stops {
			stopCoords[stop.ID] = struct{ lat, lon float64 }{lat: stop.Lat, lon: stop.Lon}
		}
	}

	return api.calculateBatchStopDistances(stopTimes, shapePoints, stopCoords, agencyID)

}

type ReferenceParams struct {
	IncludeTrip bool
	Stops       []gtfsdb.Stop
	// StopIDsByBareID maps each stop's bare ID to the combined ID the entries
	// referred to it by, so a reference is published under the ID pointing at it.
	StopIDsByBareID map[string]string
	Trips           []models.TripsForLocationListEntry
	Situations      []situationRef
}

func (api *RestAPI) BuildReference(w http.ResponseWriter, r *http.Request, ctx context.Context, params ReferenceParams) models.ReferencesModel {
	refs := &referenceBuilder{
		api:           api,
		ctx:           ctx,
		presentTrips:  make(map[string]models.Trip, len(params.Trips)),
		presentRoutes: make(map[string]models.Route),
	}

	if err := refs.build(params); err != nil {
		api.serverErrorResponse(w, r, err)
		return models.ReferencesModel{}
	}

	return refs.toReferencesModel()
}

type referenceBuilder struct {
	api             *RestAPI
	ctx             context.Context
	presentTrips    map[string]models.Trip
	presentRoutes   map[string]models.Route
	presentAgencies map[string]models.AgencyReference
	stopList        []models.Stop
	tripsRefList    []models.Trip
	situations      []situationRef
}

func (rb *referenceBuilder) build(params ReferenceParams) error {
	rb.situations = params.Situations
	rb.collectTripIDs(params.Trips)
	rb.buildStopList(params.Stops, params.StopIDsByBareID)

	rb.enrichTripsData()

	if err := rb.collectAgenciesAndRoutes(); err != nil {
		return err
	}

	if params.IncludeTrip {
		if err := rb.buildTripReferences(); err != nil {
			return err
		}
	}

	return nil
}

func (rb *referenceBuilder) collectTripIDs(trips []models.TripsForLocationListEntry) {
	for _, trip := range trips {
		_, tripID, err := utils.ExtractAgencyIDAndCodeID(trip.TripId)
		if err == nil {
			rb.presentTrips[tripID] = models.Trip{}
		}

		if trip.Schedule != nil {
			if _, nextID, err := utils.ExtractAgencyIDAndCodeID(trip.Schedule.NextTripId); err == nil {
				rb.presentTrips[nextID] = models.Trip{}
			}
			if _, prevID, err := utils.ExtractAgencyIDAndCodeID(trip.Schedule.PreviousTripId); err == nil {
				rb.presentTrips[prevID] = models.Trip{}
			}
		}

		if trip.Status != nil && trip.Status.ActiveTripID != "" {
			if _, activeID, err := utils.ExtractAgencyIDAndCodeID(trip.Status.ActiveTripID); err == nil {
				rb.presentTrips[activeID] = models.Trip{}
			}
		}

	}
}

// buildStopList emits the stop references and registers the routes serving them.
// A stop referred to by more than one agency still gets a single reference — the
// first ID seen wins, and the other stays dangling.
func (rb *referenceBuilder) buildStopList(stops []gtfsdb.Stop, stopIDsByBareID map[string]string) {
	stopList, routeIDsByStop := rb.api.stopReferences(rb.ctx, stops, stopIDsByBareID)
	rb.stopList = stopList

	// Register the raw route ID (e.g. "100479") rather than the combined one, so
	// that collectAgenciesAndRoutes can fetch full route details via
	// GetRoutesByIDs, which queries WHERE routes.id IN (?) using raw IDs.
	for _, combinedRouteIDs := range routeIDsByStop {
		for _, combinedID := range combinedRouteIDs {
			if rawID, err := utils.ExtractCodeID(combinedID); err == nil {
				rb.presentRoutes[rawID] = models.Route{}
			}
		}
	}
}

func (rb *referenceBuilder) enrichTripsData() {
	var tripIDs []string
	for id := range rb.presentTrips {
		tripIDs = append(tripIDs, id)
	}

	if len(tripIDs) == 0 {
		return
	}

	trips, err := rb.api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(rb.ctx, tripIDs)
	if err != nil {
		logging.LogError(rb.api.Logger, "failed to batch fetch trips for references", err)
		return
	}

	for _, trip := range trips {
		if _, exists := rb.presentTrips[trip.ID]; exists {
			rb.presentTrips[trip.ID] = rb.createTrip(trip)
			rb.presentRoutes[trip.RouteID] = models.Route{}
		}
	}
}

func (rb *referenceBuilder) createTrip(trip gtfsdb.Trip) models.Trip {
	return models.Trip{
		ID:            trip.ID,
		RouteID:       trip.RouteID,
		ServiceID:     trip.ServiceID,
		TripHeadsign:  trip.TripHeadsign.String,
		TripShortName: trip.TripShortName.String,
		DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
		BlockID:       trip.BlockID.String,
		ShapeID:       trip.ShapeID.String,
		PeakOffPeak:   0,
		TimeZone:      "",
	}
}

func (rb *referenceBuilder) collectAgenciesAndRoutes() error {
	rb.presentAgencies = make(map[string]models.AgencyReference)

	var routeIDs []string
	for id := range rb.presentRoutes {
		routeIDs = append(routeIDs, id)
	}

	if len(routeIDs) == 0 {
		return nil
	}

	routes, err := rb.api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(rb.ctx, routeIDs)
	if err != nil {
		return err
	}

	agencyIDSet := make(map[string]struct{})
	for _, route := range routes {
		rb.presentRoutes[route.ID] = rb.createRoute(route)
		agencyIDSet[route.AgencyID] = struct{}{}
	}

	uniqueAgencyIDs := make([]string, 0, len(agencyIDSet))
	for id := range agencyIDSet {
		uniqueAgencyIDs = append(uniqueAgencyIDs, id)
	}

	agencies, err := rb.api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(rb.ctx, uniqueAgencyIDs)
	if err != nil {
		return err
	}

	for _, agency := range agencies {
		rb.presentAgencies[agency.ID] = models.AgencyReferenceFromDatabase(&agency)
	}
	return nil
}

func (rb *referenceBuilder) createRoute(route gtfsdb.Route) models.Route {
	return models.NewRoute(
		utils.FormCombinedID(route.AgencyID, route.ID),
		route.AgencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String)

}

func (rb *referenceBuilder) buildTripReferences() error {
	rb.tripsRefList = make([]models.Trip, 0, len(rb.presentTrips))

	for _, trip := range rb.presentTrips {
		if rb.ctx.Err() != nil {
			return rb.ctx.Err()
		}

		if trip.ID == "" {
			continue
		}

		route, ok := rb.presentRoutes[trip.RouteID]
		if !ok {
			continue
		}
		rb.tripsRefList = append(rb.tripsRefList, rb.createTripReference(trip, route.AgencyID))
	}
	return nil
}

func (rb *referenceBuilder) createTripReference(trip models.Trip, currentAgency string) models.Trip {
	return models.Trip{
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
	}
}

func (rb *referenceBuilder) toReferencesModel() models.ReferencesModel {
	trips := rb.tripsRefList
	if trips == nil {
		trips = []models.Trip{}
	}
	stops := rb.stopList
	if stops == nil {
		stops = []models.Stop{}
	}

	references := models.NewEmptyReferences()
	references.Agencies = rb.getAgenciesList()
	references.Routes = rb.getRoutesList()
	references.Stops = stops
	references.Trips = trips
	references.Situations = rb.getSituationsList()

	return *references
}

func (rb *referenceBuilder) getSituationsList() []models.Situation {
	return rb.api.situationReferences(rb.situations)
}

// scheduleData bundles pre-fetched inputs for buildScheduleFromMemory.
type scheduleData struct {
	trip            gtfsdb.Trip
	agencyID        string
	currentLocation *time.Location
	stopTimes       []gtfsdb.StopTime
	shapePoints     []gtfs.ShapePoint
	stopCoords      map[string]struct{ lat, lon float64 }
	blockTrips      []gtfsdb.GetTripsByBlockIDsRow
}

// buildScheduleFromMemory constructs a TripsSchedule from pre-fetched stop times, shape points, and block trips.
// frequency is the caller's pre-computed entry frequency (nil when the trip has none).
func (api *RestAPI) buildScheduleFromMemory(data scheduleData, frequency *models.Frequency) *models.TripsSchedule {

	// Calculate Next/Prev using in-memory block trips
	nextTripID, previousTripID := api.calculateNextPrevFromMemory(data.trip, data.blockTrips, data.agencyID)

	// Calculate Distances using in-memory coords
	stopTimesList := api.calculateBatchStopDistances(data.stopTimes, data.shapePoints, data.stopCoords, data.agencyID)

	return &models.TripsSchedule{
		Frequency:      frequency,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       data.currentLocation.String(),
	}
}

// calculateNextPrevFromMemory determines the next and previous trip IDs within a block.
func (api *RestAPI) calculateNextPrevFromMemory(currentTrip gtfsdb.Trip, blockTrips []gtfsdb.GetTripsByBlockIDsRow, agencyID string) (string, string) {
	if len(blockTrips) == 0 {
		return "", ""
	}

	// Filter blockTrips to only include those that share the exact ServiceID of the current trip.
	// This ensures we don't mix trips from different service days (e.g. Weekday vs Weekend).
	var relevantTrips []gtfsdb.GetTripsByBlockIDsRow
	for _, t := range blockTrips {
		if t.ServiceID == currentTrip.ServiceID {
			relevantTrips = append(relevantTrips, t)
		}
	}

	if len(relevantTrips) == 0 {
		return "", ""
	}

	// Find index of current trip in the ordered list
	currentIndex := -1
	for i, t := range relevantTrips {
		if t.ID == currentTrip.ID {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return "", ""
	}

	var next, prev string

	// BlockTrips are already ordered by departure_time via the SQL query (GetTripsByBlockIDs)
	if currentIndex < len(relevantTrips)-1 {
		next = utils.FormCombinedID(agencyID, relevantTrips[currentIndex+1].ID)
	}
	if currentIndex > 0 {
		prev = utils.FormCombinedID(agencyID, relevantTrips[currentIndex-1].ID)
	}

	return next, prev
}
