package restapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// arrivalsForLocationParams holds the validated query for
// arrivals-and-departures-for-location.
type arrivalsForLocationParams struct {
	Location             *internalgtfs.LocationParams
	QueryTime            time.Time
	TimeParam            string
	Before               time.Duration
	After                time.Duration
	MaxCount             int
	RouteTypes           []int
	EmptyReturnsNotFound bool
	IncludeReferences    bool
}

// locationDateTimeLayout is the only datetime format the time parameter
// accepts besides epoch millis.
const locationDateTimeLayout = "2006-01-02_15-04-05"

// maxRouteTypeValues bounds how many routeType values one request may send, so
// a pathological query string cannot expand the per-route filter unboundedly.
const maxRouteTypeValues = 100

func (api *RestAPI) arrivalsAndDeparturesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	params, fieldErrors := api.parseArrivalsForLocationParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// One snapshot cache for the whole request. Trip status runs only for the
	// arrivals that survive maxCount, and those rows still share block snapshots.
	ctx := WithSnapshotCache(r.Context(), newSnapshotCache())

	// Uncapped and clamped: Java computes arrivals for every stop in the box and
	// only trims the three output lists at the end, so capping the stop query
	// here would silently drop arrivals that belong in the response.
	stops, err := api.GtfsManager.GetStopsInBounds(ctx, params.Location, 0, true)
	if err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}
	if len(stops) == 0 {
		api.sendEmptyArrivalsForLocation(w, r, params)
		return
	}

	agencies, err := api.agenciesForStops(ctx, stops)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// The service date is a local calendar date, so datetime values resolve in
	// the fallback agency's timezone. The format was validated during parsing,
	// so this resolution cannot fail.
	params.QueryTime = resolveLocationQueryTime(params, agencies, api.Clock.Now())

	// Nearest stop first: truncation below drops entries from the end, so the
	// farthest stops must sort last.
	sortStopsByDistance(stops, params.Location)

	acc := newArrivalsAccumulator("")
	pending, err := api.arrivalsForStops(ctx, multiStopArrivalsInput{
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

	// Nearest stop first, then the shared best-arrival comparison. Neither needs
	// trip status. Trip, route and agency alerts are recorded for every row
	// before the trim, so a dropped arrival still contributes its alerts.
	distances := stopDistances(stops, agencies, params.Location)
	sortStatusPendingArrivals(pending.Arrivals, distances)
	api.addPendingArrivalAlerts(pending.Arrivals, acc)
	arrivals := make([]models.ArrivalAndDeparture, len(pending.Arrivals))
	for i := range pending.Arrivals {
		arrivals[i] = pending.Arrivals[i].arrival
	}

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

	if err := api.finishRetainedLocationArrivals(ctx, lists, pending, stops, agencies, acc); err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}

	// Note: no empty check here. stops is non-empty (empty searches return
	// earlier) and maxCount is at least 1, so stopIDs is never empty and a
	// stop list with zero arrivals is a normal response, not an empty one.

	// A route can serve a retained stop and still have every arrival fall
	// outside the window. Those route alerts belong on the entry anyway.
	if err := api.addRouteAlertsForRetainedStops(ctx, lists.stopIDs, acc.situations); err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
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

	references, err := api.locationReferences(ctx, params.IncludeReferences, agencies, refAcc)
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

// finishRetainedLocationArrivals builds trip status for the arrivals that
// survived maxCount, then records stop-level alerts for every searched stop.
// A window that matched no stop_time still carries those stop alerts. Dropped
// rows never reach BuildTripStatus.
func (api *RestAPI) finishRetainedLocationArrivals(
	ctx context.Context,
	lists locationLists,
	pending multiStopArrivalsResult,
	stops []gtfsdb.Stop,
	agencies *stopAgencyIndex,
	acc *arrivalsAccumulator,
) error {
	if err := api.finishArrivalTripStatus(ctx, lists.arrivals, pending.Arrivals[:len(lists.arrivals)], acc); err != nil {
		return err
	}
	return api.addStopLevelArrivalAlerts(ctx, stops, agencies, acc)
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

// addRouteAlertsForRetainedStops records alerts for every route that serves a
// retained stop. Agency and route ids stay in separate columns: an agency id
// can contain an underscore, so the combined agency_code form is not split
// back apart. Alerts are indexed by the bare route id.
func (api *RestAPI) addRouteAlertsForRetainedStops(ctx context.Context, combinedStopIDs []string, situations *situationCollector) error {
	stopIDs := retainedStopCodes(combinedStopIDs)
	if len(stopIDs) == 0 {
		return nil
	}

	rows, err := utils.QueryInBatches(ctx, stopIDs, api.GtfsManager.GtfsDB.Queries.GetRoutesForStops)
	if err != nil {
		return err
	}

	seen := make(map[routeAlertKey]struct{})
	for _, row := range rows {
		key := routeAlertKey{agencyID: row.AgencyID, routeID: row.ID}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		situations.add(api.GtfsManager.GetAlertsForRoute(row.ID), row.AgencyID)
	}
	return nil
}

// retainedStopCodes returns each bare stop id in combinedIDs once, in order.
// A value that is not an agency_code id is skipped.
func retainedStopCodes(combinedIDs []string) []string {
	codes := make([]string, 0, len(combinedIDs))
	seen := make(map[string]struct{}, len(combinedIDs))
	for _, id := range combinedIDs {
		_, code, err := utils.ExtractAgencyIDAndCodeID(id)
		if err != nil || code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes
}

// routeAlertKey is one route serving a retained stop. The same route is
// returned once per stop it serves, and only the first copy is recorded.
type routeAlertKey struct {
	agencyID string
	routeID  string
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
//
// The retained arrivals are a reference source in their own right, not just a
// filter over the stop lists: maxCount trims stopIDs and arrivals
// independently, so an arrival can outlive its own stop's place in stopIDs,
// and a trip status names stops and a reassigned trip that no list carries.
// Every ID an arrival emits has to resolve, so all of them are retained here.
func retainedAccumulator(
	arrivals []models.ArrivalAndDeparture,
	stopIDs []string,
	nearby []models.StopWithDistance,
	acc *arrivalsAccumulator,
) *arrivalsAccumulator {
	refAcc := newArrivalsAccumulator("")
	refAcc.situations = acc.situations

	for _, id := range stopIDs {
		retainStop(refAcc, id)
	}
	for _, n := range nearby {
		retainStop(refAcc, n.StopID)
	}

	for _, a := range arrivals {
		retainStop(refAcc, a.StopID)
		retainTrip(refAcc, acc, a.TripID)
		retainRoute(refAcc, acc, a.RouteID)
		retainTripStatusEntities(refAcc, acc, a.TripStatus)
	}

	return refAcc
}

// retainTripStatusEntities retains the stops and the reassigned active trip a
// trip status points at. recordTripStatusReferences resolved these into acc
// while the arrival was built, so nothing here needs a query.
func retainTripStatusEntities(refAcc, acc *arrivalsAccumulator, status *models.TripStatus) {
	if status == nil {
		return
	}
	retainStop(refAcc, status.NextStop)
	retainStop(refAcc, status.ClosestStop)
	retainTrip(refAcc, acc, status.ActiveTripID)
}

// retainStop records a combined stop ID under the bare ID appendStopReferences
// looks it up by. A malformed or empty ID is skipped.
func retainStop(refAcc *arrivalsAccumulator, combinedStopID string) {
	if _, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedStopID); err == nil {
		refAcc.stopIDs[bareID] = true
	}
}

// retainTrip carries a combined trip ID's entity over from acc, which already
// resolved it. A trip missing from acc is skipped rather than queried: it would
// have had to come from a row that never reached the accumulator.
func retainTrip(refAcc, acc *arrivalsAccumulator, combinedTripID string) {
	if _, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedTripID); err == nil {
		if trip, ok := acc.trips[bareID]; ok {
			refAcc.trips[bareID] = trip
		}
	}
}

// retainRoute carries a combined route ID's entity over from acc, under the
// same contract as retainTrip.
func retainRoute(refAcc, acc *arrivalsAccumulator, combinedRouteID string) {
	if _, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedRouteID); err == nil {
		if route, ok := acc.routes[bareID]; ok {
			refAcc.routes[bareID] = route
		}
	}
}

// locationReferences builds the references block, or an empty one when the
// caller opted out with includeReferences=false.
func (api *RestAPI) locationReferences(
	ctx context.Context,
	includeReferences bool,
	agencies *stopAgencyIndex,
	acc *arrivalsAccumulator,
) (*models.ReferencesModel, error) {
	if !includeReferences {
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

// nearbyBoxHalfSizeMeters is the Java nearby-stops half box: the union of the
// stops within a 100 m square box around each matched stop (each excluding
// itself).
const nearbyBoxHalfSizeMeters = 100.0

// nearbyStopsForLocation mirrors the Java nearby-stops rule: the union of the
// stops within a 100 m square box around each matched stop (each excluding
// itself), limited to stops served by a route running on the query date,
// measured against the centre of the search area and ordered nearest first.
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
	candidates, err := api.GtfsManager.GetStopsInBounds(ctx, expandLocationForNearby(params.Location), 0, true)
	if err != nil {
		return nil, err
	}
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

	// Java drops nearby stops served only by routes not running on the query
	// date (e.g. a seasonal shuttle outside its season). The active-date
	// result is kept per route so the route-type filter below can require
	// the same route to satisfy both conditions.
	activeRoutesByStop, err := api.nearbyStopsActiveOnDate(ctx, bareIDs, agencies, params.QueryTime)
	if err != nil {
		return nil, err
	}
	activeOnDate := activeStopsFromRoutes(activeRoutesByStop)

	servesRouteType, err := api.stopsServingRouteTypes(ctx, bareIDs, params.RouteTypes, activeRoutesByStop)
	if err != nil {
		return nil, err
	}

	return buildNearbyResults(nearbyStops, servesRouteType, activeOnDate, combinedByBare, params.Location), nil
}

// nearbyCandidateIDs returns the union of the stops within a 100 m square box
// around a matched stop, excluding each stop itself.
func nearbyCandidateIDs(ctx context.Context, candidates, stops []gtfsdb.Stop) (map[string]bool, error) {
	boxes := make(map[string]utils.CoordinateBounds, len(stops))
	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		boxes[stop.ID] = utils.CalculateBounds(stop.Lat, stop.Lon, nearbyBoxHalfSizeMeters)
	}
	nearIDs := make(map[string]bool)
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, stop := range stops {
			if stop.ID == candidate.ID {
				continue
			}
			if utils.BoundsContain(boxes[stop.ID], candidate.Lat, candidate.Lon) {
				nearIDs[candidate.ID] = true
				break
			}
		}
	}
	return nearIDs, nil
}

// combinedIDsForNearbyStops resolves every candidate to its combined agency
// ID in one batch query, falling back per stop to the matched-stop agency
// index. It also records each candidate's agency and timezone in the shared
// index so the active-date filter can group stops by their own agency
// timezone instead of the matched stops' fallback.
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
		if _, exists := agencies.byStopID[row.StopID]; !exists {
			agencies.byStopID[row.StopID] = row.ID
		}
		if _, exists := agencies.locations[row.ID]; !exists {
			agencies.locations[row.ID] = agencyLocationOrUTC(ctx, row.ID, row.Timezone)
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
		expanded.Radius = radius + nearbyBoxHalfSizeMeters
		return &expanded
	}

	margin := utils.CalculateBounds(loc.Lat, loc.Lon, nearbyBoxHalfSizeMeters)
	expanded.LatSpan = loc.LatSpan + (margin.MaxLat - margin.MinLat)
	expanded.LonSpan = loc.LonSpan + (margin.MaxLon - margin.MinLon)
	return &expanded
}

// nearbyStopsActiveOnDate reports which routes serve each stop on the query
// date's service day in that stop's own agency timezone. Stops sharing a
// timezone are queried together so one request still costs one service-date
// lookup per timezone rather than one per stop.
func (api *RestAPI) nearbyStopsActiveOnDate(ctx context.Context, stopIDs []string, agencies *stopAgencyIndex, queryTime time.Time) (map[string]map[string]bool, error) {
	active := make(map[string]map[string]bool, len(stopIDs))
	if len(stopIDs) == 0 {
		return active, nil
	}

	groups, err := groupStopsByLocation(ctx, stopIDs, agencies)
	if err != nil {
		return nil, err
	}

	for loc, ids := range groups {
		if err := api.collectActiveRoutesForGroup(ctx, loc, ids, queryTime, active); err != nil {
			return nil, err
		}
	}
	return active, nil
}

// groupStopsByLocation buckets stops by their own agency timezone so each
// bucket shares one service date.
func groupStopsByLocation(ctx context.Context, stopIDs []string, agencies *stopAgencyIndex) (map[*time.Location][]string, error) {
	groups := make(map[*time.Location][]string)
	for _, stopID := range stopIDs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		loc := agencies.locationFor(stopID)
		groups[loc] = append(groups[loc], stopID)
	}
	return groups, nil
}

// collectActiveRoutesForGroup records the routes serving one timezone
// bucket on its local service date.
func (api *RestAPI) collectActiveRoutesForGroup(ctx context.Context, loc *time.Location, ids []string, queryTime time.Time, active map[string]map[string]bool) error {
	serviceIDs, err := api.activeServiceIDsForLocation(ctx, loc, queryTime)
	if err != nil {
		return err
	}
	if len(serviceIDs) == 0 {
		return nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetActiveRouteIDsForStopsOnDate(ctx, gtfsdb.GetActiveRouteIDsForStopsOnDateParams{
		StopIds:    ids,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		recordActiveRoute(active, row.StopID, row.RouteID)
	}
	return nil
}

// activeServiceIDsForLocation resolves the service IDs running on the
// query date in the given agency timezone.
func (api *RestAPI) activeServiceIDsForLocation(ctx context.Context, loc *time.Location, queryTime time.Time) ([]string, error) {
	dateStr := queryTime.In(loc).Format("20060102")
	return api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, dateStr)
}

// recordActiveRoute keeps one agency-prefixed route ID for a stop,
// ignoring rows that do not carry a well-formed combined ID.
func recordActiveRoute(active map[string]map[string]bool, stopID string, routeID interface{}) {
	combinedID, ok := routeID.(string)
	if !ok {
		return
	}
	if _, _, err := utils.ExtractAgencyIDAndCodeID(combinedID); err != nil {
		return
	}
	routes, ok := active[stopID]
	if !ok {
		routes = make(map[string]bool)
		active[stopID] = routes
	}
	routes[combinedID] = true
}

// activeStopsFromRoutes reduces per-route activity to the per-stop boolean
// buildNearbyResults expects.
func activeStopsFromRoutes(activeRoutesByStop map[string]map[string]bool) map[string]bool {
	active := make(map[string]bool, len(activeRoutesByStop))
	for stopID := range activeRoutesByStop {
		active[stopID] = true
	}
	return active
}

// stopsServingRouteTypes reports which of the given stops are served by at
// least one active route of an allowed type. The active set holds the
// agency-prefixed route IDs running on the query date, so a stop served by
// one route of the right type and another route on the right date is not
// kept unless a single route satisfies both. It returns nil when no filter
// is active, which callers read as "keep everything" rather than
// "keep nothing".
func (api *RestAPI) stopsServingRouteTypes(ctx context.Context, stopIDs []string, routeTypes []int, activeRoutesByStop map[string]map[string]bool) (map[string]bool, error) {
	if len(routeTypes) == 0 {
		return nil, nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	matching := make(map[string]bool, len(stopIDs))
	for _, row := range rows {
		if !isRouteTypeAllowed(row.Type, routeTypes) {
			continue
		}
		if !activeRoutesByStop[row.StopID][utils.FormCombinedID(row.AgencyID, row.ID)] {
			continue
		}
		matching[row.StopID] = true
	}
	return matching, nil
}

// sortStopsByDistance orders stops nearest the search centre first, breaking
// ties on bare stop ID so the order is deterministic.
func sortStopsByDistance(stops []gtfsdb.Stop, loc *internalgtfs.LocationParams) {
	slices.SortStableFunc(stops, func(a, b gtfsdb.Stop) int {
		if d := utils.Distance(loc.Lat, loc.Lon, a.Lat, a.Lon) - utils.Distance(loc.Lat, loc.Lon, b.Lat, b.Lon); d != 0 {
			return cmp.Compare(d, 0)
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// sortStatusPendingArrivals orders arrivals before trip status is built.
// Nearest stop first, then compareBestArrival within a stop. The pending
// slice and the copied arrivals must stay aligned so finishArrivalTripStatus
// attaches status to the right row.
func sortStatusPendingArrivals(arrivals []statusPendingArrival, distances map[string]float64) {
	slices.SortStableFunc(arrivals, func(a, b statusPendingArrival) int {
		if d := distances[a.arrival.StopID] - distances[b.arrival.StopID]; d != 0 {
			return cmp.Compare(d, 0)
		}
		return compareBestArrival(a.arrival, b.arrival)
	})
}

// addPendingArrivalAlerts records trip, route and agency alerts for every
// arrival in the box, including rows maxCount will drop. arrivalInput already
// carries those IDs, so this does not build trip status or query the database.
func (api *RestAPI) addPendingArrivalAlerts(rows []statusPendingArrival, acc *arrivalsAccumulator) {
	for _, row := range rows {
		agencyID := row.input.route.AgencyID
		alerts := api.GtfsManager.GetAlertsByIDs(row.input.stopTime.TripID, row.input.route.ID, agencyID)
		acc.situations.add(alerts, agencyID)
	}
}

// stopDistances maps each matched stop's combined ID to its distance from the
// search centre, so arrivals sort by how near their stop is.
func stopDistances(stops []gtfsdb.Stop, agencies *stopAgencyIndex, loc *internalgtfs.LocationParams) map[string]float64 {
	distances := make(map[string]float64, len(stops))
	for _, stop := range stops {
		combined := utils.FormCombinedID(agencies.agencyIDFor(stop.ID), stop.ID)
		distances[combined] = utils.Distance(loc.Lat, loc.Lon, stop.Lat, stop.Lon)
	}
	return distances
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
			index.locations[row.ID] = agencyLocationOrUTC(ctx, row.ID, row.Timezone)
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
func agencyLocationOrUTC(ctx context.Context, agencyID, timezone string) *time.Location {
	loc, err := loadAgencyLocation(agencyID, timezone)
	if err != nil {
		logging.ForComponent(ctx, "http_server").Warn("failed to load agency timezone, falling back to UTC",
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
		QueryTime:         api.Clock.Now(),
		Before:            5 * time.Minute,
		After:             35 * time.Minute,
		MaxCount:          models.DefaultMaxCountForArrivalsForLocation,
		IncludeReferences: true,
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
	// Only epoch millis and yyyy-MM-dd_HH-mm-ss are accepted. In particular
	// signed values and date-only values fail here, at parse time, so a bad
	// time reports 400 even when the stop search finds nothing. The datetime
	// itself resolves after agencies are known; see the handler.
	params.TimeParam = queryParams.Get("time")
	if params.TimeParam != "" {
		if _, ok := utils.ParseEpochMillis(params.TimeParam); !ok {
			if _, err := time.ParseInLocation(locationDateTimeLayout, params.TimeParam, time.UTC); err != nil {
				addError("time", `Invalid field value for field "time".`)
			}
		}
	}
	params.MaxCount = parseArrivalsForLocationMaxCount(queryParams, addError)
	params.RouteTypes = parseRouteTypesParam(queryParams, addError)
	params.EmptyReturnsNotFound, fieldErrors = utils.ParseBoolParam(queryParams, "emptyReturnsNotFound", false, fieldErrors)
	params.IncludeReferences, fieldErrors = ShouldIncludeReferences(r, fieldErrors)

	return params, fieldErrors
}

// resolveLocationQueryTime turns the validated time parameter into the query
// instant. Epoch millis are absolute; datetime values read in the fallback
// agency's timezone. An empty value falls back to now.
func resolveLocationQueryTime(params arrivalsForLocationParams, agencies *stopAgencyIndex, now time.Time) time.Time {
	if params.TimeParam == "" {
		return now
	}
	if ms, ok := utils.ParseEpochMillis(params.TimeParam); ok {
		return time.UnixMilli(ms)
	}
	parsed, _ := time.ParseInLocation(locationDateTimeLayout, params.TimeParam, agencies.fallbackLocation)
	return parsed
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
