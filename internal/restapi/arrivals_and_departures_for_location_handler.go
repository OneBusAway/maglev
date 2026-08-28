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
	arrivals, err := api.collectArrivalsForStops(ctx, stops, agencies, params, acc)
	if err != nil {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
		} else {
			api.serverErrorResponse(w, r, err)
		}
		return
	}

	stopIDs := make([]string, 0, len(stops))
	for _, stop := range stops {
		stopIDs = append(stopIDs, utils.FormCombinedID(agencies.agencyIDFor(stop.ID), stop.ID))
	}

	nearby := api.nearbyStopsForLocation(ctx, stops, agencies, params)

	sortArrivalsByTime(arrivals)

	// Java trims all three lists against the single maxCount and reports one
	// flag if any of them was truncated.
	limitExceeded := false
	stopIDs, limitExceeded = truncateSlice(stopIDs, params.MaxCount, limitExceeded)
	arrivals, limitExceeded = truncateSlice(arrivals, params.MaxCount, limitExceeded)
	nearby, limitExceeded = truncateSlice(nearby, params.MaxCount, limitExceeded)

	if len(arrivals) == 0 && len(stopIDs) == 0 {
		api.sendEmptyArrivalsForLocation(w, r, params)
		return
	}

	// Every stop the response names must resolve in references.stops, including
	// the nearby ones (Java's BeanFactoryV2 adds them alongside the arrivals').
	for _, stop := range stops {
		acc.stopIDs[stop.ID] = true
	}
	for _, n := range nearby {
		if _, bareID, err := utils.ExtractAgencyIDAndCodeID(n.StopID); err == nil {
			acc.stopIDs[bareID] = true
		}
	}

	references := models.NewEmptyReferences()
	if ShouldIncludeReferences(r) {
		references, err = api.buildArrivalsReferences(ctx, arrivalsReferencesInput{
			fallbackAgencyID: agencies.fallbackAgencyID,
			stopAgencies:     agencies.byStopID,
		}, acc)
		if err != nil {
			if ctx.Err() != nil {
				api.clientCanceledResponse(w, r, ctx.Err())
			} else {
				api.serverErrorResponse(w, r, err)
			}
			return
		}
		references.Situations = append(references.Situations, api.situationReferences(acc.situations.refs)...)
	}

	response := models.NewArrivalsAndDeparturesForLocationResponse(
		arrivals,
		*references,
		stopIDs,
		nearby,
		situationIDsFromRefs(acc.situations.refs),
		limitExceeded,
		api.Clock,
	)
	api.sendResponse(w, r, response)
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
	api.sendResponse(w, r, models.NewArrivalsAndDeparturesForLocationResponse(
		nil, *models.NewEmptyReferences(), nil, nil, nil, false, api.Clock))
}

// collectArrivalsForStops runs the shared per-stop arrivals pipeline over every
// stop in the search area, merging what they reference into acc.
func (api *RestAPI) collectArrivalsForStops(
	ctx context.Context,
	stops []gtfsdb.Stop,
	agencies *stopAgencyIndex,
	params arrivalsForLocationParams,
	acc *arrivalsAccumulator,
) ([]models.ArrivalAndDeparture, error) {
	arrivals := make([]models.ArrivalAndDeparture, 0)

	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		agencyID := agencies.agencyIDFor(stop.ID)
		location := agencies.locationFor(stop.ID)

		result, err := api.arrivalsForStop(ctx, stopArrivalsInput{
			StopCode:   stop.ID,
			AgencyID:   agencyID,
			Location:   location,
			QueryTime:  params.QueryTime.In(location),
			Before:     params.Before,
			After:      params.After,
			RouteTypes: params.RouteTypes,
		}, acc)
		if err != nil {
			return nil, err
		}

		arrivals = append(arrivals, result.Arrivals...)
		acc.situations.add(api.GtfsManager.GetAlertsForStop(stop.ID), agencyID)
	}

	return arrivals, nil
}

// nearbyStopsForLocation mirrors the Java nearby-stops rule: the union of the
// stops within 100 m of each matched stop (each excluding itself), measured
// against the centre of the search area and ordered nearest first.
//
// It is deliberately not "every stop in the bounding box" — a matched stop with
// no neighbour within 100 m does not appear, while a stop just outside the box
// does if it neighbours one that is inside.
func (api *RestAPI) nearbyStopsForLocation(
	ctx context.Context,
	stops []gtfsdb.Stop,
	agencies *stopAgencyIndex,
	params arrivalsForLocationParams,
) []models.StopWithDistance {
	combinedIDsByBareID := make(map[string]string)
	for _, stop := range stops {
		for _, combinedID := range getNearbyStopIDs(api, ctx, stop.Lat, stop.Lon, stop.ID, agencies.agencyIDFor(stop.ID)) {
			if _, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedID); err == nil {
				combinedIDsByBareID[bareID] = combinedID
			}
		}
	}
	if len(combinedIDsByBareID) == 0 {
		return nil
	}

	bareIDs := make([]string, 0, len(combinedIDsByBareID))
	for bareID := range combinedIDsByBareID {
		bareIDs = append(bareIDs, bareID)
	}

	nearbyStops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, bareIDs)
	if err != nil {
		api.Logger.Warn("failed to fetch nearby stops for location", "error", err)
		return nil
	}

	servesRouteType := api.stopsServingRouteTypes(ctx, bareIDs, params.RouteTypes)

	results := make([]models.StopWithDistance, 0, len(nearbyStops))
	for _, stop := range nearbyStops {
		if servesRouteType != nil && !servesRouteType[stop.ID] {
			continue
		}
		results = append(results, models.StopWithDistance{
			StopID:            combinedIDsByBareID[stop.ID],
			DistanceFromQuery: utils.Distance(params.Location.Lat, params.Location.Lon, stop.Lat, stop.Lon),
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

// stopsServingRouteTypes reports which of the given stops are served by at
// least one route of an allowed type. It returns nil when no filter is active,
// which callers read as "keep everything" rather than "keep nothing".
func (api *RestAPI) stopsServingRouteTypes(ctx context.Context, stopIDs []string, routeTypes []int) map[string]bool {
	if len(routeTypes) == 0 {
		return nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		api.Logger.Warn("failed to fetch routes while filtering nearby stops by routeType", "error", err)
		return map[string]bool{}
	}

	matching := make(map[string]bool, len(stopIDs))
	for _, row := range rows {
		if isRouteTypeAllowed(row.Type, routeTypes) {
			matching[row.StopID] = true
		}
	}
	return matching
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
			loc, locErr := loadAgencyLocation(row.ID, row.Timezone)
			if locErr != nil {
				api.Logger.Warn("failed to load agency timezone, falling back to UTC",
					"agencyID", row.ID, "error", locErr)
				loc = time.UTC
			}
			index.locations[row.ID] = loc
		}
	}

	for agencyID, count := range counts {
		best := counts[index.fallbackAgencyID]
		if count > best || (count == best && agencyID < index.fallbackAgencyID) {
			index.fallbackAgencyID = agencyID
		}
	}
	if loc, ok := index.locations[index.fallbackAgencyID]; ok && loc != nil {
		index.fallbackLocation = loc
	}

	return index, nil
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

	// parseLocationParams treats lat and lon as optional; this endpoint's spec
	// marks them required, and defaulting them to 0 would silently search the
	// Gulf of Guinea.
	for _, key := range []string{"lat", "lon"} {
		if queryParams.Get(key) == "" {
			addError(key, "required")
		}
	}

	location, locationErrors := api.parseLocationParams(r, nil)
	for field, msgs := range locationErrors {
		for _, msg := range msgs {
			addError(field, msg)
		}
	}
	params.Location = location

	params.Before = parseMinutesParam(queryParams, "minutesBefore", params.Before, addError)
	params.After = parseMinutesParam(queryParams, "minutesAfter", params.After, addError)

	if val := queryParams.Get("time"); val != "" {
		if timeMs, err := strconv.ParseInt(val, 10, 64); err == nil {
			params.QueryTime = time.UnixMilli(timeMs)
		} else {
			addError("time", "must be a valid Unix timestamp in milliseconds")
		}
	}

	maxCount, maxCountErrors := utils.ParseMaxCountClampedTo(
		queryParams, models.DefaultMaxCountForArrivalsForLocation, models.MaxCountForArrivalsForLocation, nil)
	for field, msgs := range maxCountErrors {
		for _, msg := range msgs {
			addError(field, msg)
		}
	}
	params.MaxCount = maxCount

	params.RouteTypes = parseRouteTypesParam(queryParams, addError)

	if val := queryParams.Get("emptyReturnsNotFound"); val != "" {
		emptyReturnsNotFound, err := strconv.ParseBool(val)
		if err != nil {
			addError("emptyReturnsNotFound", "must be a valid boolean")
		}
		params.EmptyReturnsNotFound = emptyReturnsNotFound
	}

	return params, fieldErrors
}

// parseMinutesParam reads a minute-valued window parameter, capping it at one
// service day to bound the per-request stop_time scan.
func parseMinutesParam(queryParams map[string][]string, key string, fallback time.Duration, addError func(string, string)) time.Duration {
	const maxWindow = 24 * time.Hour

	values, ok := queryParams[key]
	if !ok || len(values) == 0 || values[0] == "" {
		return fallback
	}

	minutes, err := strconv.Atoi(values[0])
	if err != nil {
		addError(key, "must be a valid integer")
		return fallback
	}
	if minutes < 0 {
		addError(key, "must be a non-negative integer")
		return fallback
	}
	return min(time.Duration(minutes)*time.Minute, maxWindow)
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
