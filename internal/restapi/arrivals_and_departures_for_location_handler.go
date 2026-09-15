package restapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// arrivalsForLocationParams holds the validated query for
// arrivals-and-departures-for-location.
type arrivalsForLocationParams struct {
	Location             *internalgtfs.LocationParams
	QueryTime            time.Time
	Before               time.Duration
	After                time.Duration
	MaxCount             int
	RouteTypes           []int
	EmptyReturnsNotFound bool
}

// maxRouteTypeValues bounds how many routeType values one request may send, so
// a pathological query string cannot expand the per-route filter unboundedly.
const maxRouteTypeValues = 100

func (api *RestAPI) arrivalsAndDeparturesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	params, fieldErrors := api.parseArrivalsForLocationParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// One snapshot cache for the whole request: BuildTripStatus runs once per
	// arrival row across every stop in the box, and without sharing the cache
	// the block computation is repeated for each of them.
	ctx := WithSnapshotCache(r.Context(), newSnapshotCache())

	// Uncapped and clamped: Java computes arrivals for every stop in the box and
	// only trims the three output lists at the end, so capping the stop query
	// here would silently drop arrivals that belong in the response.
	stops := api.GtfsManager.GetStopsInBounds(ctx, params.Location, 0, true)
	if len(stops) == 0 {
		api.sendEmptyArrivalsForLocation(w, r, params)
		return
	}

	agencies, err := api.agenciesForStops(ctx, stops)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	acc := newArrivalsAccumulator("")
	arrivals, err := api.arrivalsForStops(ctx, multiStopArrivalsInput{
		Stops:      stops,
		Agencies:   agencies,
		QueryTime:  params.QueryTime,
		Before:     params.Before,
		After:      params.After,
		RouteTypes: params.RouteTypes,
	}, acc)
	if err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}

	sortArrivalsByTime(arrivals)

	nearby, err := api.nearbyStopsForLocation(ctx, stops, agencies, params)
	if err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}

	lists := truncateLocationLists(locationLists{
		stopIDs:  combinedStopIDs(stops, agencies),
		arrivals: arrivals,
		nearby:   nearby,
	}, params.MaxCount)

	if len(lists.arrivals) == 0 && len(lists.stopIDs) == 0 {
		api.sendEmptyArrivalsForLocation(w, r, params)
		return
	}

	// Record each nearby stop's agency so references namespace it exactly as
	// nearbyStopIds does, instead of falling back to the matched stops' agency.
	for _, n := range lists.nearby {
		if agencyID, bareID, err := utils.ExtractAgencyIDAndCodeID(n.StopID); err == nil {
			if _, exists := agencies.byStopID[bareID]; !exists {
				agencies.byStopID[bareID] = agencyID
			}
		}
	}

	// References cover only the retained entry results, not the stops and
	// arrivals maxCount trimmed away.
	refAcc := retainedAccumulator(lists.arrivals, lists.stopIDs, lists.nearby, acc)

	references, err := api.locationReferences(ctx, r, agencies, refAcc)
	if err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}

	api.sendResponse(w, r, models.NewArrivalsAndDeparturesForLocationResponse(
		lists.arrivals,
		*references,
		lists.stopIDs,
		lists.nearby,
		situationIDsFromRefs(refAcc.situations.refs),
		lists.limitExceeded,
		api.Clock,
	))
}

// sendArrivalsForLocationError distinguishes the client hanging up from a
// genuine server-side failure, so a cancelled request is not reported as a 500.
func (api *RestAPI) sendArrivalsForLocationError(w http.ResponseWriter, r *http.Request, ctx context.Context, err error) {
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}
	api.serverErrorResponse(w, r, err)
}

// locationLists are the three response lists that share a single maxCount.
type locationLists struct {
	stopIDs       []string
	arrivals      []models.ArrivalAndDeparture
	nearby        []models.StopWithDistance
	limitExceeded bool
}

// truncateLocationLists trims each list to maxCount independently, reporting a
// single flag if any of them was shortened — matching how Java caps this
// endpoint.
func truncateLocationLists(lists locationLists, maxCount int) locationLists {
	lists.stopIDs, lists.limitExceeded = truncateSlice(lists.stopIDs, maxCount, lists.limitExceeded)
	lists.arrivals, lists.limitExceeded = truncateSlice(lists.arrivals, maxCount, lists.limitExceeded)
	lists.nearby, lists.limitExceeded = truncateSlice(lists.nearby, maxCount, lists.limitExceeded)
	return lists
}

// combinedStopIDs renders the searched stops as {agency}_{code} IDs.
func combinedStopIDs(stops []gtfsdb.Stop, agencies *stopAgencyIndex) []string {
	stopIDs := make([]string, 0, len(stops))
	for _, stop := range stops {
		stopIDs = append(stopIDs, utils.FormCombinedID(agencies.agencyIDFor(stop.ID), stop.ID))
	}
	return stopIDs
}

// retainedAccumulator rebuilds an accumulator holding only what the truncated
// entry lists reference, so references do not serialize the stops, trips and
// routes maxCount trimmed away. Situation references stay global: alerts are
// few, and tracking them per trip would require invasive accumulator changes.
func retainedAccumulator(
	arrivals []models.ArrivalAndDeparture,
	stopIDs []string,
	nearby []models.StopWithDistance,
	acc *arrivalsAccumulator,
) *arrivalsAccumulator {
	refAcc := newArrivalsAccumulator("")
	refAcc.situations = acc.situations

	for _, id := range stopIDs {
		if _, bareID, err := utils.ExtractAgencyIDAndCodeID(id); err == nil {
			refAcc.stopIDs[bareID] = true
		}
	}
	for _, n := range nearby {
		if _, bareID, err := utils.ExtractAgencyIDAndCodeID(n.StopID); err == nil {
			refAcc.stopIDs[bareID] = true
		}
	}

	for _, a := range arrivals {
		if _, bareTripID, err := utils.ExtractAgencyIDAndCodeID(a.TripID); err == nil {
			if trip, ok := acc.trips[bareTripID]; ok {
				refAcc.trips[bareTripID] = trip
			}
		}
		if _, bareRouteID, err := utils.ExtractAgencyIDAndCodeID(a.RouteID); err == nil {
			if route, ok := acc.routes[bareRouteID]; ok {
				refAcc.routes[bareRouteID] = route
			}
		}
	}

	return refAcc
}

// locationReferences builds the references block, or an empty one when the
// caller opted out with includeReferences=false.
func (api *RestAPI) locationReferences(
	ctx context.Context,
	r *http.Request,
	agencies *stopAgencyIndex,
	acc *arrivalsAccumulator,
) (*models.ReferencesModel, error) {
	if !ShouldIncludeReferences(r) {
		return models.NewEmptyReferences(), nil
	}

	references, err := api.buildArrivalsReferences(ctx, arrivalsReferencesInput{
		fallbackAgencyID: agencies.fallbackAgencyID,
		stopAgencies:     agencies.byStopID,
	}, acc)
	if err != nil {
		return nil, err
	}

	references.Situations = append(references.Situations, api.situationReferences(ctx, acc.situations.refs)...)
	return references, nil
}

// sendEmptyArrivalsForLocation answers a query that matched nothing. Unlike the
// Java server, which serialises a bare bean here and so emits a different shape
// than the populated case, the envelope stays identical because the OpenAPI
// schema marks entry and references required.
func (api *RestAPI) sendEmptyArrivalsForLocation(w http.ResponseWriter, r *http.Request, params arrivalsForLocationParams) {
	if params.EmptyReturnsNotFound {
		api.sendNotFound(w, r)
		return
	}
	api.sendResponse(w, r, models.NewEmptyArrivalsAndDeparturesForLocationResponse(api.Clock))
}

// nearbyRadiusMeters is the Java nearby-stops radius: the union of the stops
// within 100 m of each matched stop (each excluding itself).
const nearbyRadiusMeters = 100.0

// nearbyStopsForLocation mirrors the Java nearby-stops rule: the union of the
// stops within 100 m of each matched stop (each excluding itself), limited to
// stops served by a route running on the query date, measured against the
// centre of the search area and ordered nearest first.
//
// It is deliberately not "every stop in the bounding box" — a matched stop with
// no neighbour within 100 m does not appear, while a stop just outside the box
// does if it neighbours one that is inside. One expanded spatial query covers
// all matched stops instead of one lookup per stop.
func (api *RestAPI) nearbyStopsForLocation(
	ctx context.Context,
	stops []gtfsdb.Stop,
	agencies *stopAgencyIndex,
	params arrivalsForLocationParams,
) ([]models.StopWithDistance, error) {
	candidates := api.GtfsManager.GetStopsInBounds(ctx, expandLocationForNearby(params.Location), 0, true)
	if len(candidates) == 0 {
		return nil, nil
	}

	nearIDs, err := nearbyCandidateIDs(ctx, candidates, stops)
	if err != nil {
		return nil, err
	}
	if len(nearIDs) == 0 {
		return nil, nil
	}

	bareIDs := make([]string, 0, len(nearIDs))
	for bareID := range nearIDs {
		bareIDs = append(bareIDs, bareID)
	}

	combinedByBare, err := api.combinedIDsForNearbyStops(ctx, bareIDs, agencies)
	if err != nil {
		return nil, err
	}

	nearbyStops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, bareIDs)
	if err != nil {
		return nil, err
	}

	servesRouteType, err := api.stopsServingRouteTypes(ctx, bareIDs, params.RouteTypes)
	if err != nil {
		return nil, err
	}

	// Java drops nearby stops served only by routes not running on the query
	// date (e.g. a seasonal shuttle outside its season).
	activeOnDate, err := api.nearbyStopsActiveOnDate(ctx, bareIDs, agencies, params.QueryTime)
	if err != nil {
		return nil, err
	}

	return buildNearbyResults(nearbyStops, servesRouteType, activeOnDate, combinedByBare, params.Location), nil
}

// nearbyCandidateIDs returns the union of the stops within 100 m of a matched
// stop, excluding each stop itself.
func nearbyCandidateIDs(ctx context.Context, candidates, stops []gtfsdb.Stop) (map[string]bool, error) {
	nearIDs := make(map[string]bool)
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, stop := range stops {
			if stop.ID == candidate.ID {
				continue
			}
			if utils.Distance(candidate.Lat, candidate.Lon, stop.Lat, stop.Lon) <= nearbyRadiusMeters {
				nearIDs[candidate.ID] = true
				break
			}
		}
	}
	return nearIDs, nil
}

// combinedIDsForNearbyStops resolves every candidate to its combined agency
// ID in one batch query, falling back per stop to the matched-stop agency
// index.
func (api *RestAPI) combinedIDsForNearbyStops(ctx context.Context, bareIDs []string, agencies *stopAgencyIndex) (map[string]string, error) {
	combinedByBare := make(map[string]string, len(bareIDs))
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, bareIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range agencyRows {
		if _, exists := combinedByBare[row.StopID]; !exists {
			combinedByBare[row.StopID] = utils.FormCombinedID(row.ID, row.StopID)
		}
	}
	for _, bareID := range bareIDs {
		if _, exists := combinedByBare[bareID]; !exists {
			combinedByBare[bareID] = utils.FormCombinedID(agencies.agencyIDFor(bareID), bareID)
		}
	}
	return combinedByBare, nil
}

// buildNearbyResults keeps the nearby stops passing the route-type and
// active-on-date filters, measuring each against the centre of the search
// area and ordering nearest first. A nil servesRouteType means no route-type
// filter is active, so every stop is kept rather than none.
func buildNearbyResults(nearbyStops []gtfsdb.Stop, servesRouteType map[string]bool, activeOnDate map[string]bool, combinedByBare map[string]string, loc *internalgtfs.LocationParams) []models.StopWithDistance {
	results := make([]models.StopWithDistance, 0, len(nearbyStops))
	for _, stop := range nearbyStops {
		if servesRouteType != nil && !servesRouteType[stop.ID] {
			continue
		}
		if !activeOnDate[stop.ID] {
			continue
		}
		results = append(results, models.StopWithDistance{
			StopID:            combinedByBare[stop.ID],
			DistanceFromQuery: utils.Distance(loc.Lat, loc.Lon, stop.Lat, stop.Lon),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].DistanceFromQuery != results[j].DistanceFromQuery {
			return results[i].DistanceFromQuery < results[j].DistanceFromQuery
		}
		return results[i].StopID < results[j].StopID
	})
	return results
}

// expandLocationForNearby returns a copy of loc whose search box covers the
// original box plus a 100 m margin on every side, so one uncapped spatial query
// fetches every candidate nearby stop. The active location mode is preserved:
// radius mode grows the radius, span mode grows the spans.
func expandLocationForNearby(loc *internalgtfs.LocationParams) *internalgtfs.LocationParams {
	expanded := *loc
	if loc.Radius > 0 || !(loc.LatSpan > 0 && loc.LonSpan > 0) {
		radius := loc.Radius
		if radius <= 0 {
			radius = models.DefaultSearchRadiusInMeters
		}
		expanded.Radius = radius + nearbyRadiusMeters
		return &expanded
	}

	margin := utils.CalculateBounds(loc.Lat, loc.Lon, nearbyRadiusMeters)
	expanded.LatSpan = loc.LatSpan + (margin.MaxLat - margin.MinLat)
	expanded.LonSpan = loc.LonSpan + (margin.MaxLon - margin.MinLon)
	return &expanded
}

// nearbyStopsActiveOnDate reports which of the given stops are served by at
// least one route running on the query date's service day in the fallback
// agency's timezone.
func (api *RestAPI) nearbyStopsActiveOnDate(ctx context.Context, stopIDs []string, agencies *stopAgencyIndex, queryTime time.Time) (map[string]bool, error) {
	active := make(map[string]bool, len(stopIDs))
	if len(stopIDs) == 0 {
		return active, nil
	}

	dateStr := queryTime.In(agencies.fallbackLocation).Format("20060102")
	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, dateStr)
	if err != nil {
		return nil, err
	}
	if len(serviceIDs) == 0 {
		return active, nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetActiveRouteIDsForStopsOnDate(ctx, gtfsdb.GetActiveRouteIDsForStopsOnDateParams{
		StopIds:    stopIDs,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		active[row.StopID] = true
	}
	return active, nil
}

// stopsServingRouteTypes reports which of the given stops are served by at
// least one route of an allowed type. It returns nil when no filter is active,
// which callers read as "keep everything" rather than "keep nothing".
func (api *RestAPI) stopsServingRouteTypes(ctx context.Context, stopIDs []string, routeTypes []int) (map[string]bool, error) {
	if len(routeTypes) == 0 {
		return nil, nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	matching := make(map[string]bool, len(stopIDs))
	for _, row := range rows {
		if isRouteTypeAllowed(row.Type, routeTypes) {
			matching[row.StopID] = true
		}
	}
	return matching, nil
}

// sortArrivalsByTime orders arrivals by when a rider would actually see them,
// preferring the predicted time when one exists.
func sortArrivalsByTime(arrivals []models.ArrivalAndDeparture) {
	effectiveTime := func(a models.ArrivalAndDeparture) int64 {
		if a.PredictedArrivalTime.UnixMilli() > 0 {
			return a.PredictedArrivalTime.UnixMilli()
		}
		return a.ScheduledArrivalTime.UnixMilli()
	}
	sort.SliceStable(arrivals, func(i, j int) bool {
		return effectiveTime(arrivals[i]) < effectiveTime(arrivals[j])
	})
}

// truncateSlice trims items to maxCount, reporting whether anything was dropped
// OR-ed with the flag it was handed.
func truncateSlice[T any](items []T, maxCount int, limitExceeded bool) ([]T, bool) {
	if len(items) <= maxCount {
		return items, limitExceeded
	}
	return items[:maxCount], true
}

// stopAgencyIndex resolves which agency owns a stop, and that agency's
// timezone, for every stop matched by one request.
type stopAgencyIndex struct {
	byStopID         map[string]string
	locations        map[string]*time.Location
	fallbackAgencyID string
	fallbackLocation *time.Location
}

func (s *stopAgencyIndex) agencyIDFor(stopID string) string {
	if agencyID, ok := s.byStopID[stopID]; ok && agencyID != "" {
		return agencyID
	}
	return s.fallbackAgencyID
}

func (s *stopAgencyIndex) locationFor(stopID string) *time.Location {
	if loc, ok := s.locations[s.agencyIDFor(stopID)]; ok && loc != nil {
		return loc
	}
	return s.fallbackLocation
}

// agenciesForStops resolves each stop's owning agency and timezone in one
// batch. The most common agency becomes the fallback, so stops that resolve to
// nothing still get a sensible namespace and service day.
func (api *RestAPI) agenciesForStops(ctx context.Context, stops []gtfsdb.Stop) (*stopAgencyIndex, error) {
	stopIDs := make([]string, 0, len(stops))
	for _, stop := range stops {
		stopIDs = append(stopIDs, stop.ID)
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	index := &stopAgencyIndex{
		byStopID:         make(map[string]string, len(rows)),
		locations:        make(map[string]*time.Location),
		fallbackLocation: time.UTC,
	}

	counts := make(map[string]int)
	for _, row := range rows {
		// The query orders by stop then agency, so the first agency for a stop
		// served by several is stable across requests.
		if _, exists := index.byStopID[row.StopID]; !exists {
			index.byStopID[row.StopID] = row.ID
		}
		counts[row.ID]++

		if _, exists := index.locations[row.ID]; !exists {
			index.locations[row.ID] = api.agencyLocationOrUTC(row.ID, row.Timezone)
		}
	}

	index.fallbackAgencyID = mostCommonAgency(counts)
	if loc, ok := index.locations[index.fallbackAgencyID]; ok && loc != nil {
		index.fallbackLocation = loc
	}

	return index, nil
}

// agencyLocationOrUTC resolves an agency's timezone, degrading to UTC rather
// than failing the request over one unparseable timezone string.
func (api *RestAPI) agencyLocationOrUTC(agencyID, timezone string) *time.Location {
	loc, err := loadAgencyLocation(agencyID, timezone)
	if err != nil {
		api.Logger.Warn("failed to load agency timezone, falling back to UTC",
			"agencyID", agencyID, "error", err)
		return time.UTC
	}
	return loc
}

// mostCommonAgency picks the agency serving the most stops, breaking ties on
// ID so the choice does not depend on map iteration order.
func mostCommonAgency(counts map[string]int) string {
	best := ""
	for agencyID, count := range counts {
		if count > counts[best] || (count == counts[best] && agencyID < best) {
			best = agencyID
		}
	}
	return best
}

func (api *RestAPI) parseArrivalsForLocationParams(r *http.Request) (arrivalsForLocationParams, map[string][]string) {
	queryParams := r.URL.Query()

	params := arrivalsForLocationParams{
		QueryTime: api.Clock.Now(),
		Before:    5 * time.Minute,
		After:     35 * time.Minute,
		MaxCount:  models.DefaultMaxCountForArrivalsForLocation,
	}

	var fieldErrors map[string][]string
	addError := func(field, msg string) {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		fieldErrors[field] = append(fieldErrors[field], msg)
	}

	params.Location = &internalgtfs.LocationParams{}
	params.Location.Lat, fieldErrors = utils.ParseRequiredFloatParam(queryParams, "lat", fieldErrors)
	params.Location.Lon, fieldErrors = utils.ParseRequiredFloatParam(queryParams, "lon", fieldErrors)
	params.Location.Radius, fieldErrors = utils.ParseFloatParam(queryParams, "radius", fieldErrors)
	params.Location.LatSpan, fieldErrors = utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	params.Location.LonSpan, fieldErrors = utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)

	for field, msgs := range utils.ValidateLocationParams(
		params.Location.Lat, params.Location.Lon,
		params.Location.Radius, params.Location.LatSpan, params.Location.LonSpan,
	) {
		for _, msg := range msgs {
			addError(field, msg)
		}
	}

	params.Before = parseMinutesValue(queryParams, "minutesBefore", params.Before, maxArrivalWindow, addError)
	params.After = parseMinutesValue(queryParams, "minutesAfter", params.After, maxArrivalWindow, addError)
	params.QueryTime = parseEpochMillisValue(queryParams, "time", params.QueryTime, addError)
	params.MaxCount = parseArrivalsForLocationMaxCount(queryParams, addError)
	params.RouteTypes = parseRouteTypesParam(queryParams, addError)
	params.EmptyReturnsNotFound, fieldErrors = utils.ParseBoolParam(queryParams, "emptyReturnsNotFound", false, fieldErrors)

	return params, fieldErrors
}

func parseArrivalsForLocationMaxCount(queryParams map[string][]string, addError func(string, string)) int {
	maxCount, fieldErrors := utils.ParseMaxCountClampedTo(
		queryParams, models.DefaultMaxCountForArrivalsForLocation, models.MaxCountForArrivalsForLocation, nil)
	for field, msgs := range fieldErrors {
		for _, msg := range msgs {
			addError(field, msg)
		}
	}
	return maxCount
}

// parseRouteTypesParam reads the comma-delimited routeType filter. Note this
// filters arrivals and nearby stops only — matching Java, the stop search
// itself is unfiltered, so stopIds is unaffected by routeType.
func parseRouteTypesParam(queryParams map[string][]string, addError func(string, string)) []int {
	values, ok := queryParams["routeType"]
	if !ok || len(values) == 0 || values[0] == "" {
		return nil
	}

	tokens := strings.Split(values[0], ",")
	if len(tokens) > maxRouteTypeValues {
		addError("routeType", "too many values")
		return nil
	}

	routeTypes := make([]int, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		routeType, err := strconv.Atoi(token)
		if err != nil {
			addError("routeType", "must be a comma-delimited list of integers")
			return nil
		}
		routeTypes = append(routeTypes, routeType)
	}
	return routeTypes
}
