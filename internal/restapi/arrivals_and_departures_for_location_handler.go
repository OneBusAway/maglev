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
	// trip status.
	distances := stopDistances(stops, agencies, params.Location)
	sortStatusPendingArrivals(pending.Arrivals, distances)
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

	// Entry situations are the ones Java puts on the entry: stop alerts for
	// every found stop and route alerts for the stops that survive maxCount.
	// An arrival's own alerts never belong here — they ride on that arrival
	// and in the references.
	entrySituations := newSituationCollector()

	if err := api.finishRetainedLocationArrivals(ctx, lists, pending, stops, agencies, acc, entrySituations); err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}

	// Note: no empty check here. stops is non-empty (empty searches return
	// earlier) and maxCount is at least 1, so stopIDs is never empty and a
	// stop list with zero arrivals is a normal response, not an empty one.

	// A route can serve a retained stop and still have every arrival fall
	// outside the window. Those route alerts belong on the entry anyway.
	if err := api.addRouteAlertsForRetainedStops(ctx, lists.stopIDs, entrySituations, agencies); err != nil {
		api.sendArrivalsForLocationError(w, r, ctx, err)
		return
	}

	// References cover only the retained entry results, not the stops and
	// arrivals maxCount trimmed away.
	refAcc := retainedAccumulator(lists.arrivals, lists.stopIDs, lists.nearby, acc, entrySituations, agencies)

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
		situationIDsFromRefs(entrySituations.refs),
		lists.limitExceeded,
		api.Clock,
	))
}

// finishRetainedLocationArrivals builds trip status for the arrivals that
// survived maxCount, then records stop-level alerts for every searched stop.
// A window that matched no stop_time still carries those stop alerts. Dropped
// rows never reach BuildTripStatus, so their trip alerts are not collected.
func (api *RestAPI) finishRetainedLocationArrivals(
	ctx context.Context,
	lists locationLists,
	pending multiStopArrivalsResult,
	stops []gtfsdb.Stop,
	agencies *stopAgencyIndex,
	acc *arrivalsAccumulator,
	entrySituations *situationCollector,
) error {
	if err := api.finishArrivalTripStatus(ctx, lists.arrivals, pending.Arrivals[:len(lists.arrivals)], acc); err != nil {
		return err
	}
	return api.addStopLevelArrivalAlerts(ctx, stops, agencies, entrySituations)
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
func (api *RestAPI) addRouteAlertsForRetainedStops(ctx context.Context, combinedStopIDs []string, situations *situationCollector, agencies *stopAgencyIndex) error {
	stopIDs := retainedStopCodes(combinedStopIDs, agencies)
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
// A value that does not match a known stop is skipped.
func retainedStopCodes(combinedIDs []string, agencies *stopAgencyIndex) []string {
	stopIndex := buildCombinedStopIndex(nil, agencies)
	agenciesList := knownAgencies(nil, agencies)
	codes := make([]string, 0, len(combinedIDs))
	seen := make(map[string]struct{}, len(combinedIDs))
	for _, id := range combinedIDs {
		var bareID string
		if stopIndex != nil {
			bareID = stopIndex[id]
		}
		if bareID == "" {
			for _, agencyID := range agenciesList {
				prefix := agencyID + "_"
				if strings.HasPrefix(id, prefix) {
					bareID = strings.TrimPrefix(id, prefix)
					break
				}
			}
		}
		if bareID == "" {
			if _, code, err := utils.ExtractAgencyIDAndCodeID(id); err == nil && code != "" {
				bareID = code
			}
		}
		if bareID == "" {
			continue
		}
		if _, ok := seen[bareID]; ok {
			continue
		}
		seen[bareID] = struct{}{}
		codes = append(codes, bareID)
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
// routes maxCount trimmed away.
//
// The situations are the entry's own plus the retained arrivals' own: an
// arrival that maxCount dropped is not in the response, so nothing it
// referenced belongs in the references either.
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
	entrySituations *situationCollector,
	agencies *stopAgencyIndex,
) *arrivalsAccumulator {
	refAcc := newArrivalsAccumulator("")
	refAcc.situations = newSituationCollector()
	refAcc.situations.addRefs(entrySituations.refs)
	refAcc.situations.addRefs(acc.situations.refs)

	stopIndex := buildCombinedStopIndex(acc, agencies)
	tripIndex := buildCombinedTripIndex(acc, agencies)
	routeIndex := buildCombinedRouteIndex(acc, agencies)
	agenciesList := knownAgencies(acc, agencies)

	for _, id := range stopIDs {
		retainStop(refAcc, id, stopIndex, agenciesList)
	}
	for _, n := range nearby {
		retainStop(refAcc, n.StopID, stopIndex, agenciesList)
	}

	for _, a := range arrivals {
		retainStop(refAcc, a.StopID, stopIndex, agenciesList)
		retainTrip(refAcc, acc, a.TripID, tripIndex)
		retainRoute(refAcc, a.RouteID, routeIndex)
		retainTripStatusEntities(refAcc, acc, a.TripStatus, stopIndex, tripIndex, agenciesList)
	}

	return refAcc
}

// retainTripStatusEntities retains the stops and the reassigned active trip a
// trip status points at. recordTripStatusReferences resolved these into acc
// while the arrival was built, so nothing here needs a query.
func retainTripStatusEntities(
	refAcc *arrivalsAccumulator,
	acc *arrivalsAccumulator,
	status *models.TripStatus,
	stopIndex map[string]string,
	tripIndex map[string]*gtfsdb.Trip,
	agenciesList []string,
) {
	if status == nil {
		return
	}
	retainStop(refAcc, status.NextStop, stopIndex, agenciesList)
	retainStop(refAcc, status.ClosestStop, stopIndex, agenciesList)
	retainTrip(refAcc, acc, status.ActiveTripID, tripIndex)
}

// retainStop records a combined stop ID under the bare ID appendStopReferences
// looks it up by. A malformed or empty ID is skipped.
func retainStop(refAcc *arrivalsAccumulator, combinedStopID string, stopIndex map[string]string, knownAgencies []string) {
	if stopIndex != nil {
		if bareID, ok := stopIndex[combinedStopID]; ok {
			refAcc.stopIDs[bareID] = true
			return
		}
	}
	for _, agencyID := range knownAgencies {
		if agencyID != "" {
			prefix := agencyID + "_"
			if strings.HasPrefix(combinedStopID, prefix) {
				if bareID := strings.TrimPrefix(combinedStopID, prefix); bareID != "" {
					refAcc.stopIDs[bareID] = true
					return
				}
			}
		}
	}
	if _, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedStopID); err == nil && bareID != "" {
		refAcc.stopIDs[bareID] = true
	}
}

// retainTrip carries a combined trip ID's entity over from acc, which already
// resolved it. It also retains the trip's route so appendTripReferences does
// not repeat the database lookup.
func retainTrip(refAcc *arrivalsAccumulator, acc *arrivalsAccumulator, combinedTripID string, tripIndex map[string]*gtfsdb.Trip) {
	if tripIndex == nil {
		return
	}
	if trip, ok := tripIndex[combinedTripID]; ok {
		refAcc.trips[trip.ID] = trip
		if acc != nil {
			if route, ok := acc.routes[trip.RouteID]; ok {
				refAcc.routes[route.ID] = route
			}
		}
	}
}

// retainRoute carries a combined route ID's entity over from acc, under the
// same contract as retainTrip.
func retainRoute(refAcc *arrivalsAccumulator, combinedRouteID string, routeIndex map[string]*gtfsdb.Route) {
	if routeIndex == nil {
		return
	}
	if route, ok := routeIndex[combinedRouteID]; ok {
		refAcc.routes[route.ID] = route
	}
}

// knownAgencies returns all distinct agency IDs involved in the request,
// sorted by descending length so that longer agency prefixes match before
// prefixes that are substrings.
func knownAgencies(acc *arrivalsAccumulator, agencies *stopAgencyIndex) []string {
	seen := make(map[string]struct{})
	if agencies != nil {
		if agencies.fallbackAgencyID != "" {
			seen[agencies.fallbackAgencyID] = struct{}{}
		}
		for agencyID := range agencies.locations {
			if agencyID != "" {
				seen[agencyID] = struct{}{}
			}
		}
	}
	if acc != nil {
		for _, route := range acc.routes {
			if route.AgencyID != "" {
				seen[route.AgencyID] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) != len(result[j]) {
			return len(result[i]) > len(result[j])
		}
		return result[i] < result[j]
	})
	return result
}

// addStopIndices adds combined IDs for bareIDs under agencyID to index.
func addStopIndices(index map[string]string, bareIDs []string, agencyID string) {
	for _, bareID := range bareIDs {
		comb := utils.FormCombinedID(agencyID, bareID)
		if _, exists := index[comb]; !exists {
			index[comb] = bareID
		}
	}
}

// buildCombinedStopIndex maps each stop's combined ID to its bare ID.
// If multiple stops map to the same combined ID, the lexicographically
// smallest bare ID is retained.
func buildCombinedStopIndex(acc *arrivalsAccumulator, agencies *stopAgencyIndex) map[string]string {
	if agencies == nil {
		return nil
	}
	stopSet := make(map[string]struct{}, len(agencies.byStopID))
	for bareID := range agencies.byStopID {
		stopSet[bareID] = struct{}{}
	}
	if acc != nil {
		for bareID := range acc.stopIDs {
			stopSet[bareID] = struct{}{}
		}
	}
	bareIDs := make([]string, 0, len(stopSet))
	for bareID := range stopSet {
		bareIDs = append(bareIDs, bareID)
	}
	sort.Strings(bareIDs)

	index := make(map[string]string, len(bareIDs))
	for _, bareID := range bareIDs {
		agencyID := agencies.agencyIDFor(bareID)
		comb := utils.FormCombinedID(agencyID, bareID)
		if _, exists := index[comb]; !exists {
			index[comb] = bareID
		}
	}
	for _, agencyID := range knownAgencies(acc, agencies) {
		addStopIndices(index, bareIDs, agencyID)
	}
	return index
}

// buildCombinedTripIndex maps each trip's combined ID to its *gtfsdb.Trip.
// If multiple trips map to the same combined ID, the lexicographically
// smallest bare ID is retained.
func buildCombinedTripIndex(acc *arrivalsAccumulator, agencies *stopAgencyIndex) map[string]*gtfsdb.Trip {
	if acc == nil || len(acc.trips) == 0 {
		return nil
	}
	index := make(map[string]*gtfsdb.Trip, len(acc.trips))
	bareIDs := make([]string, 0, len(acc.trips))
	for bareID := range acc.trips {
		bareIDs = append(bareIDs, bareID)
	}
	sort.Strings(bareIDs)
	for _, bareID := range bareIDs {
		trip := acc.trips[bareID]
		agencyID := ""
		if route, ok := acc.routes[trip.RouteID]; ok {
			agencyID = route.AgencyID
		}
		if agencyID == "" && agencies != nil {
			agencyID = agencies.fallbackAgencyID
		}
		comb := utils.FormCombinedID(agencyID, trip.ID)
		if _, exists := index[comb]; !exists {
			index[comb] = trip
		}
	}
	for _, agencyID := range knownAgencies(acc, agencies) {
		for _, bareID := range bareIDs {
			comb := utils.FormCombinedID(agencyID, bareID)
			if _, exists := index[comb]; !exists {
				index[comb] = acc.trips[bareID]
			}
		}
	}
	return index
}

// buildCombinedRouteIndex maps each route's combined ID to its *gtfsdb.Route.
// If multiple routes map to the same combined ID, the lexicographically
// smallest bare ID is retained.
func buildCombinedRouteIndex(acc *arrivalsAccumulator, agencies *stopAgencyIndex) map[string]*gtfsdb.Route {
	if acc == nil || len(acc.routes) == 0 {
		return nil
	}
	index := make(map[string]*gtfsdb.Route, len(acc.routes))
	bareIDs := make([]string, 0, len(acc.routes))
	for bareID := range acc.routes {
		bareIDs = append(bareIDs, bareID)
	}
	sort.Strings(bareIDs)
	for _, bareID := range bareIDs {
		route := acc.routes[bareID]
		agencyID := route.AgencyID
		if agencyID == "" && agencies != nil {
			agencyID = agencies.fallbackAgencyID
		}
		comb := utils.FormCombinedID(agencyID, route.ID)
		if _, exists := index[comb]; !exists {
			index[comb] = route
		}
	}
	for _, agencyID := range knownAgencies(acc, agencies) {
		for _, bareID := range bareIDs {
			comb := utils.FormCombinedID(agencyID, bareID)
			if _, exists := index[comb]; !exists {
				index[comb] = acc.routes[bareID]
			}
		}
	}
	return index
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
			agencyID := agencies.agencyIDFor(bareID)
			combinedByBare[bareID] = utils.FormCombinedID(agencyID, bareID)
			if _, exists := agencies.byStopID[bareID]; !exists && agencyID != "" {
				agencies.byStopID[bareID] = agencyID
			}
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
		recordActiveRoute(active, row.StopID, row.AgencyID, row.RouteID)
	}
	return nil
}

// activeServiceIDsForLocation resolves the service IDs running on the
// query date in the given agency timezone.
func (api *RestAPI) activeServiceIDsForLocation(ctx context.Context, loc *time.Location, queryTime time.Time) ([]string, error) {
	dateStr := queryTime.In(loc).Format("20060102")
	return api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, dateStr)
}

// recordActiveRoute keeps one agency-prefixed route ID for a stop.
func recordActiveRoute(active map[string]map[string]bool, stopID, agencyID, routeID string) {
	combinedID := utils.FormCombinedID(agencyID, routeID)
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
