package restapi

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/servicedate"
	"maglev.onebusaway.org/internal/utils"
)

// stopArrivalsInput identifies one stop and the window to compute arrivals over.
// Location is the agency timezone the window is anchored in; QueryTime must
// already be expressed in it.
type stopArrivalsInput struct {
	StopCode        string
	AgencyID        string
	Location        *time.Location
	QueryTime       time.Time
	Before          time.Duration
	After           time.Duration
	FrequencyBefore time.Duration
	FrequencyAfter  time.Duration
	RouteTypes      []int // nil or empty means no route-type filter
}

// arrivalsAccumulator gathers the entities that the arrivals of one or more
// stops reference, so a caller looping over several stops builds a single
// deduplicated references block. Construct it with newArrivalsAccumulator.
type arrivalsAccumulator struct {
	trips      map[string]*gtfsdb.Trip
	routes     map[string]*gtfsdb.Route
	stopIDs    map[string]bool
	situations *situationCollector

	// alertAgencyID is the fallback agency the single-stop handler passes to
	// situations.add for the stop-level alert lookup. It starts as that
	// handler's primary agency and, only when that is empty, adopts the first
	// route agency seen. A multi-stop caller must not rely on this field —
	// it is single-caller by design and must pass a per-stop agency ID to
	// situations.add directly instead.
	alertAgencyID string
}

func newArrivalsAccumulator(primaryAgencyID string) *arrivalsAccumulator {
	return &arrivalsAccumulator{
		trips:         make(map[string]*gtfsdb.Trip),
		routes:        make(map[string]*gtfsdb.Route),
		stopIDs:       make(map[string]bool),
		situations:    newSituationCollector(),
		alertAgencyID: primaryAgencyID,
	}
}

// stopArrivalsResult is what one stop contributed to a request.
type stopArrivalsResult struct {
	Arrivals []models.ArrivalAndDeparture

	// Matched reports whether any stop_time fell inside the window at all,
	// which is distinct from Arrivals being empty (every matched row can still
	// be dropped for a missing route or trip). The per-stop handler short-
	// circuits reference and nearby-stop work when nothing matched.
	Matched bool
}

// activeStopTime pairs a stop_time row with the service date it was matched on,
// since the ±1-day window can match the same trip on adjacent service days.
type activeStopTime struct {
	gtfsdb.GetStopTimesForStopInWindowRow
	ServiceDate servicedate.Date
	Location    *time.Location
}

// arrivalsForStop computes the arrivals and departures for a single stop over
// the requested window, recording the routes, trips, stops and situations they
// reference into acc.
//
// Callers are responsible for installing a request-scoped snapshot cache
// (WithSnapshotCache) before the first call — BuildTripStatus is invoked once
// per arrival row, and across a wide window or many stops the uncached compute
// chain dominates the request.
func (api *RestAPI) arrivalsForStop(ctx context.Context, in stopArrivalsInput, acc *arrivalsAccumulator) (stopArrivalsResult, error) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	stopID := utils.FormCombinedID(in.AgencyID, in.StopCode)
	result := stopArrivalsResult{Arrivals: make([]models.ArrivalAndDeparture, 0)}

	allActiveStopTimes, err := api.activeStopTimesForWindow(ctx, in)
	if err != nil {
		return result, err
	}
	if len(allActiveStopTimes) == 0 {
		return result, nil
	}
	routes, trips, tripStops, frequencies, err := api.batchArrivalEntities(ctx, allActiveStopTimes)
	if err != nil {
		return result, err
	}
	lookups := batchedArrivalLookups{routes: routes, trips: trips, tripStops: tripStops, frequencies: frequencies}

	for _, ast := range allActiveStopTimes {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		st := ast.GetStopTimesForStopInWindowRow

		before, after := in.Before, in.After
		if len(lookups.frequencies[st.TripID]) > 0 {
			before, after = in.FrequencyBefore, in.FrequencyAfter
		}
		if !api.stopTimeInArrivalWindow(ctx, ast, ast.Location, in.QueryTime, before, after) {
			continue
		}

		result.Matched = true
		acc.stopIDs[in.StopCode] = true

		route, trip, reportable := lookups.resolveRouteAndTrip(st, in.RouteTypes, reqLogger)
		if !reportable {
			continue
		}

		rCopy := route
		acc.routes[route.ID] = &rCopy
		tCopy := trip
		acc.trips[trip.ID] = &tCopy

		arrival := api.buildArrival(ctx, arrivalInput{
			stopTime:         st,
			route:            route,
			serviceDate:      ast.ServiceDate,
			location:         ast.Location,
			queryTime:        in.QueryTime,
			stopCode:         in.StopCode,
			stopID:           stopID,
			totalStopsInTrip: lookups.tripStops.stopCounts[st.TripID],
			stopPosition:     lookups.tripStops.stopPosition(st.TripID, st.StopSequence),
			freqMap:          lookups.frequencies,
		}, acc)

		result.Arrivals = append(result.Arrivals, *arrival)
	}

	return result, nil
}

// timezoneGroup holds the agencies sharing a single distinct timezone
// *time.Location. Service days and query windows depend only on the timezone,
// so one pass per group keeps the query count flat no matter how many agencies
// share that timezone.
type timezoneGroup struct {
	location *time.Location
	agencies map[string]struct{}
}

// activeStopTimesForWindow collects the stop_times falling inside the request
// window across yesterday, today and tomorrow, resolving each serving agency
// in its own timezone so trips from agencies in other timezones are not dropped.
//
// Agencies are grouped by distinct timezone: a stop served by five agencies in
// one timezone costs the same six service/window queries as a single agency.
func (api *RestAPI) activeStopTimesForWindow(ctx context.Context, in stopArrivalsInput) ([]activeStopTime, error) {
	discovery, err := api.discoverStopAgencies(ctx, in)
	if err != nil {
		return nil, err
	}
	groups, groupKeys := groupAgenciesByTimezone(discovery.locations)
	primaryGroupKey := in.Location.String()

	var allActiveStopTimes []activeStopTime
	for _, key := range groupKeys {
		groupRows, err := api.collectGroupWindow(ctx, in, discovery.routeAgency, groups[key], key == primaryGroupKey)
		if err != nil {
			return nil, err
		}
		allActiveStopTimes = append(allActiveStopTimes, groupRows...)
	}

	// Stable sort over a deterministic input order: ties on scheduled arrival
	// (e.g. two agencies' trips due the same minute) keep a fixed order
	// instead of varying per request with the map iteration order.
	slices.SortStableFunc(allActiveStopTimes, func(a, b activeStopTime) int {
		timeA := a.ServiceDate.Start(a.Location).Add(time.Duration(a.ArrivalTime))
		timeB := b.ServiceDate.Start(b.Location).Add(time.Duration(b.ArrivalTime))
		if timeA.Before(timeB) {
			return -1
		}
		if timeA.After(timeB) {
			return 1
		}
		if c := cmp.Compare(a.TripID, b.TripID); c != 0 {
			return c
		}
		return cmp.Compare(a.StopSequence, b.StopSequence)
	})

	return allActiveStopTimes, nil
}

// stopAgencyDiscovery holds what activeStopTimesForWindow learns about a stop
// before evaluating service days: which agency each route belongs to, and the
// timezone location of every agency serving the stop.
type stopAgencyDiscovery struct {
	routeAgency map[string]string
	locations   map[string]*time.Location
}

// discoverStopAgencies finds the routes and agencies serving a stop and resolves
// each agency's timezone location. Database errors during route discovery or
// agency lookup, as well as invalid timezones, are returned to fail the request
// rather than silently dropping arrivals or calculating them in the wrong timezone.
// Agencies missing entirely from the database (sql.ErrNoRows) are skipped.
func (api *RestAPI) discoverStopAgencies(ctx context.Context, in stopArrivalsInput) (stopAgencyDiscovery, error) {
	discovery := stopAgencyDiscovery{
		routeAgency: make(map[string]string),
		locations:   make(map[string]*time.Location),
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, in.StopCode)
	if err != nil {
		return stopAgencyDiscovery{}, fmt.Errorf("fetch routes for stop %s: %w", in.StopCode, err)
	}
	for _, r := range routes {
		discovery.routeAgency[r.ID] = r.AgencyID
		if _, exists := discovery.locations[r.AgencyID]; !exists {
			discovery.locations[r.AgencyID] = nil
		}
	}

	// Always ensure the stop's primary agency is represented with its known location
	discovery.locations[in.AgencyID] = in.Location

	// Resolve timezone locations for all other agencies serving this stop
	for agencyID, loc := range discovery.locations {
		if loc != nil {
			continue
		}
		agencyLoc, err := api.resolveAgencyLocation(ctx, agencyID)
		if err != nil {
			return stopAgencyDiscovery{}, err
		}
		if agencyLoc == nil {
			delete(discovery.locations, agencyID)
			continue
		}
		discovery.locations[agencyID] = agencyLoc
	}
	return discovery, nil
}

// resolveAgencyLocation loads one agency's timezone location. If the agency row
// is missing (sql.ErrNoRows), it returns (nil, nil) so the agency can be skipped.
// Real database or timezone loading errors are returned.
func (api *RestAPI) resolveAgencyLocation(ctx context.Context, agencyID string) (*time.Location, error) {
	ag, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetch agency %s for timezone resolution: %w", agencyID, err)
	}
	agLoc, err := loadAgencyLocation(ag.ID, ag.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone for agency %s: %w", ag.ID, err)
	}
	return agLoc, nil
}

// groupAgenciesByTimezone clusters agency IDs by distinct timezone and returns
// the groups with their keys in sorted order, so callers iterate deterministically.
func groupAgenciesByTimezone(locations map[string]*time.Location) (map[string]*timezoneGroup, []string) {
	groups := make(map[string]*timezoneGroup)
	for agencyID, agencyLoc := range locations {
		key := agencyLoc.String()
		group, ok := groups[key]
		if !ok {
			group = &timezoneGroup{location: agencyLoc, agencies: make(map[string]struct{})}
			groups[key] = group
		}
		group.agencies[agencyID] = struct{}{}
	}
	return groups, slices.Sorted(maps.Keys(groups))
}

// collectGroupWindow gathers the stop_times of yesterday, today and tomorrow for
// one timezone group that fall inside the request window. A day-0 failure in any
// group is returned so the request fails loud instead of emitting a 200
// with the most important day's arrivals missing; window-spillover days are
// best-effort and only log on error.
func (api *RestAPI) collectGroupWindow(ctx context.Context, in stopArrivalsInput, routeAgencyMap map[string]string, group *timezoneGroup, isPrimaryGroup bool) ([]activeStopTime, error) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	var groupRows []activeStopTime
	for dayOffset := -1; dayOffset <= 1; dayOffset++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		dayStopTimes, err := api.stopTimesForGroupServiceDay(ctx, in, group, isPrimaryGroup, routeAgencyMap, dayOffset)
		if err != nil {
			if dayOffset == 0 {
				return nil, err
			}
			reqLogger.Warn("failed to resolve services for window-spillover day, skipping",
				slog.String("timezone", group.location.String()),
				slog.Int("day_offset", dayOffset),
				slog.Any("error", err))
			continue
		}
		groupRows = append(groupRows, dayStopTimes...)
	}
	return groupRows, nil
}

// stopTimesForGroupServiceDay returns the stop_times of a single service day for one
// timezone group that fall inside the window, keeping only those whose service is
// active that day and whose route belongs to the group. Rows whose route has no
// known agency are attributed to the primary group only, so they are neither
// dropped nor duplicated.
func (api *RestAPI) stopTimesForGroupServiceDay(
	ctx context.Context,
	in stopArrivalsInput,
	group *timezoneGroup,
	isPrimaryGroup bool,
	routeAgencyMap map[string]string,
	dayOffset int,
) ([]activeStopTime, error) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	agencyLoc := group.location
	// Fetch both trip windows before frequency metadata is available.
	windowStart := in.QueryTime.Add(-max(in.Before, in.FrequencyBefore))
	windowEnd := in.QueryTime.Add(max(in.After, in.FrequencyAfter))
	serviceDate := servicedate.Of(in.QueryTime.In(agencyLoc)).AddDays(dayOffset)
	serviceStart := serviceDate.Start(agencyLoc)
	serviceDateStr := serviceDate.String()

	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDateStr)
	if err != nil {
		return nil, fmt.Errorf("query active service IDs for %s: %w", serviceDateStr, err)
	}
	if len(activeServiceIDs) == 0 {
		return nil, nil
	}

	endOffset := windowEnd.Sub(serviceStart)
	if endOffset < 0 {
		return nil, nil
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForStopInWindow(ctx, gtfsdb.GetStopTimesForStopInWindowParams{
		StopID:           in.StopCode,
		WindowStartNanos: windowStart.Sub(serviceStart).Nanoseconds(),
		WindowEndNanos:   endOffset.Nanoseconds(),
	})
	if err != nil {
		reqLogger.Warn("failed to query stop times in window",
			slog.String("stopID", in.StopCode),
			slog.Any("error", err))
		return nil, nil
	}

	activeServiceIDSet := make(map[string]bool, len(activeServiceIDs))
	for _, sid := range activeServiceIDs {
		activeServiceIDSet[sid] = true
	}

	dayStopTimes := make([]activeStopTime, 0, len(stopTimes))
	for _, st := range stopTimes {
		if routeAgency, ok := routeAgencyMap[st.RouteID]; ok {
			if _, inGroup := group.agencies[routeAgency]; !inGroup {
				continue
			}
		} else if !isPrimaryGroup {
			continue
		}
		if activeServiceIDSet[st.ServiceID] {
			dayStopTimes = append(dayStopTimes, activeStopTime{
				GetStopTimesForStopInWindowRow: st,
				ServiceDate:                    serviceDate,
				Location:                       agencyLoc,
			})
		}
	}
	return dayStopTimes, nil
}

// multiStopArrivalsInput carries the per-request values the batched multi-stop
// arrivals pipeline needs.
type multiStopArrivalsInput struct {
	Stops           []gtfsdb.Stop
	Agencies        *stopAgencyIndex
	QueryTime       time.Time
	Before          time.Duration
	After           time.Duration
	FrequencyBefore time.Duration
	FrequencyAfter  time.Duration
	RouteTypes      []int // nil or empty means no route-type filter
}

// batchedActiveStopTime pairs a stop_time row matched for one stop with that
// stop's agency and query instant, so the shared row loop builds arrivals
// exactly as the per-stop pipeline would.
type batchedActiveStopTime struct {
	activeStopTime
	StopCode  string
	AgencyID  string
	Location  *time.Location
	QueryTime time.Time
}

// batchedArrivalLookups groups the four entity maps batchArrivalEntities
// resolves, so the row loop takes one lookups parameter instead of four.
type batchedArrivalLookups struct {
	routes      map[string]gtfsdb.Route
	trips       map[string]gtfsdb.Trip
	tripStops   tripStopIndex
	frequencies map[string][]gtfsdb.Frequency
}

// resolveRouteAndTrip returns the route and trip a stop_time belongs to, and
// whether the row is reportable at all. A row is dropped when either entity
// missed the batch fetch — which is a data gap worth logging, not an error —
// or when the request's route-type filter excludes its route.
func (lookups batchedArrivalLookups) resolveRouteAndTrip(st gtfsdb.GetStopTimesForStopInWindowRow, routeTypes []int, reqLogger *slog.Logger) (gtfsdb.Route, gtfsdb.Trip, bool) {
	route, routeExists := lookups.routes[st.RouteID]
	if !routeExists {
		reqLogger.Debug("skipping stop time: route not found in batch fetch",
			slog.String("routeID", st.RouteID),
			slog.String("tripID", st.TripID))
		return gtfsdb.Route{}, gtfsdb.Trip{}, false
	}

	trip, tripExists := lookups.trips[st.TripID]
	if !tripExists {
		reqLogger.Debug("skipping stop time: trip not found in batch fetch",
			slog.String("tripID", st.TripID),
			slog.String("routeID", st.RouteID))
		return gtfsdb.Route{}, gtfsdb.Trip{}, false
	}

	if !isRouteTypeAllowed(route.Type, routeTypes) {
		return gtfsdb.Route{}, gtfsdb.Trip{}, false
	}

	return route, trip, true
}

// multiStopArrivalsResult is the batched multi-stop arrivals pipeline before
// trip status is attached. Arrivals carry schedule and prediction only.
// Stop-level alerts are not collected here. The caller adds them after
// truncation, including when the window matched no stop_time.
type multiStopArrivalsResult struct {
	Arrivals []statusPendingArrival
}

// statusPendingArrival is one arrival whose times are known and whose trip
// status is not. Building that status walks the block, so the location handler
// trims to maxCount before paying for it.
type statusPendingArrival struct {
	arrival models.ArrivalAndDeparture
	input   arrivalInput
	vehicle *gtfs.Vehicle
}

// arrivalsForStops computes arrivals for every stop in the input with one
// batched pass: active service IDs resolve once per service date, stop_times
// load once per agency per day, and arrival entities resolve once for the whole
// request. Filtering and accumulator semantics match the per-stop
// arrivalsForStop pipeline it replaces for multi-stop callers.
//
// Trip status is left unset. The caller attaches it after truncation via
// finishArrivalTripStatus, then adds stop-level alerts. Callers still install a
// request-scoped snapshot cache (WithSnapshotCache) before the first call.
func (api *RestAPI) arrivalsForStops(ctx context.Context, in multiStopArrivalsInput, acc *arrivalsAccumulator) (multiStopArrivalsResult, error) {
	var result multiStopArrivalsResult

	groups := groupStopsByAgency(in.Stops, in.Agencies, in.QueryTime)

	// Active service IDs per YYYYMMDD date, shared by every group whose agency
	// resolves to the same service day.
	activeByDate := make(map[string]map[string]bool)

	allActive, err := api.loadBatchedStopTimes(ctx, in, groups, activeByDate)
	if err != nil {
		return result, err
	}

	if len(allActive) == 0 {
		return result, nil
	}

	plain := make([]activeStopTime, len(allActive))
	for i, b := range allActive {
		plain[i] = b.activeStopTime
		acc.stopIDs[b.StopCode] = true
	}

	routesLookup, tripsLookup, tripStops, freqMap, err := api.batchArrivalEntities(ctx, plain)
	if err != nil {
		return result, err
	}

	result.Arrivals, err = api.buildArrivalsFromRows(ctx, allActive, in, acc, batchedArrivalLookups{
		routes:      routesLookup,
		trips:       tripsLookup,
		tripStops:   tripStops,
		frequencies: freqMap,
	})
	if err != nil {
		return result, err
	}

	return result, nil
}

// finishArrivalTripStatus fills trip status on arrivals that survived maxCount.
// pending must be the same rows in the same order; arrivals is the response
// slice those rows were copied into. Dropped rows never reach BuildTripStatus,
// so their trip alerts are not collected.
func (api *RestAPI) finishArrivalTripStatus(ctx context.Context, arrivals []models.ArrivalAndDeparture, pending []statusPendingArrival, acc *arrivalsAccumulator) error {
	for i := range arrivals {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		api.attachTripStatus(ctx, &arrivals[i], pending[i].input, pending[i].vehicle, acc)
	}
	return nil
}

// addStopLevelArrivalAlerts records one alert set per searched stop. The
// collector is the caller's: the location endpoint keeps stop alerts on the
// entry, apart from the alerts the arrivals themselves carry.
func (api *RestAPI) addStopLevelArrivalAlerts(ctx context.Context, stops []gtfsdb.Stop, agencies *stopAgencyIndex, situations *situationCollector) error {
	for _, stop := range stops {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		agencyID := agencies.agencyIDFor(stop.ID)
		situations.add(api.GtfsManager.GetAlertsForStop(stop.ID, agencyID), agencyID)
	}
	return nil
}

// agencyGroup is one timezone-shared set of stops: a single query instant,
// window and set of service dates covers the whole group.
type agencyGroup struct {
	agencyID  string
	location  *time.Location
	queryTime time.Time
	stopIDs   []string
}

// groupStopsByAgency groups stops by agency. Groups track first-seen order so
// the pre-sort arrival sequence stays deterministic.
func groupStopsByAgency(stops []gtfsdb.Stop, agencies *stopAgencyIndex, queryTime time.Time) []agencyGroup {
	groupIndex := make(map[string]int)
	var groups []agencyGroup
	for _, stop := range stops {
		agencyID := agencies.agencyIDFor(stop.ID)
		if i, ok := groupIndex[agencyID]; ok {
			groups[i].stopIDs = append(groups[i].stopIDs, stop.ID)
			continue
		}
		location := agencies.locationFor(stop.ID)
		groupIndex[agencyID] = len(groups)
		groups = append(groups, agencyGroup{
			agencyID:  agencyID,
			location:  location,
			queryTime: queryTime.In(location),
			stopIDs:   []string{stop.ID},
		})
	}
	return groups
}

// loadBatchedStopTimes collects the active stop_times for every group, keeping
// first-seen group order, dayOffset -1..1 within a group, and query-row order
// within a day.
func (api *RestAPI) loadBatchedStopTimes(ctx context.Context, in multiStopArrivalsInput, groups []agencyGroup, activeByDate map[string]map[string]bool) ([]batchedActiveStopTime, error) {
	var allActive []batchedActiveStopTime
	for _, group := range groups {
		for dayOffset := -1; dayOffset <= 1; dayOffset++ {
			if err := api.appendBatchedStopTimesForDay(ctx, group, dayOffset, in, activeByDate, &allActive); err != nil {
				return nil, err
			}
		}
	}
	return allActive, nil
}

// appendBatchedStopTimesForDay appends one group's stop_times for one service
// day, filtered to routes serving the day and times inside the window.
func (api *RestAPI) appendBatchedStopTimesForDay(ctx context.Context, group agencyGroup, dayOffset int, in multiStopArrivalsInput, activeByDate map[string]map[string]bool, allActive *[]batchedActiveStopTime) error {
	reqLogger := logging.ForComponent(ctx, "http_server")

	if ctx.Err() != nil {
		return ctx.Err()
	}

	serviceDate := servicedate.Of(group.queryTime.In(group.location)).AddDays(dayOffset)
	serviceStart := serviceDate.Start(group.location)
	serviceDateStr := serviceDate.String()

	activeSet, err := api.activeServicesForDate(ctx, serviceDateStr, dayOffset, activeByDate)
	if err != nil {
		return err
	}
	if activeSet == nil || len(activeSet) == 0 {
		return nil
	}

	windowStartNanos := group.queryTime.Add(-max(in.Before, in.FrequencyBefore)).Sub(serviceStart).Nanoseconds()
	windowEndNanos := group.queryTime.Add(max(in.After, in.FrequencyAfter)).Sub(serviceStart).Nanoseconds()
	if windowEndNanos < 0 {
		return nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForStopsInWindow(ctx, gtfsdb.GetStopTimesForStopsInWindowParams{
		StopIds:          group.stopIDs,
		WindowStartNanos: windowStartNanos,
		WindowEndNanos:   windowEndNanos,
	})
	if err != nil {
		// Mirrors stopTimesForServiceDay: an unreadable stop_times
		// page is logged and yields no rows, even for day 0.
		reqLogger.Warn("failed to query stop times in window",
			slog.Any("error", err))
		return nil
	}

	for _, row := range rows {
		if !activeSet[row.ServiceID] {
			continue
		}
		*allActive = append(*allActive, batchedActiveStopTime{
			activeStopTime: activeStopTime{
				GetStopTimesForStopInWindowRow: convertStopsInWindowRow(row),
				ServiceDate:                    serviceDate,
			},
			StopCode:  row.StopID,
			AgencyID:  group.agencyID,
			Location:  group.location,
			QueryTime: group.queryTime,
		})
	}
	return nil
}

// activeServicesForDate resolves the active service IDs for one service date,
// memoizing them in activeByDate. A nil map (with nil error) means the caller
// should skip the day: a ±1-day window-spillover failure is best-effort, while
// a dayOffset==0 failure is fatal because the user's actual service date is
// the most important day's arrivals.
func (api *RestAPI) activeServicesForDate(ctx context.Context, serviceDateStr string, dayOffset int, activeByDate map[string]map[string]bool) (map[string]bool, error) {
	if set, ok := activeByDate[serviceDateStr]; ok {
		return set, nil
	}

	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDateStr)
	if err != nil {
		if dayOffset == 0 {
			return nil, fmt.Errorf("query active service IDs for %s: %w", serviceDateStr, err)
		}
		logging.ForComponent(ctx, "http_server").Warn("failed to resolve services for window-spillover day, skipping",
			slog.Int("day_offset", dayOffset),
			slog.Any("error", err))
		return nil, nil
	}

	set := make(map[string]bool, len(activeServiceIDs))
	for _, sid := range activeServiceIDs {
		set[sid] = true
	}
	activeByDate[serviceDateStr] = set
	return set, nil
}

// buildArrivalsFromRows converts the batched stop_times into arrivals,
// registering the routes and trips the rows need. Trip status is not built
// here; the caller does that after maxCount truncation.
func (api *RestAPI) buildArrivalsFromRows(ctx context.Context, allActive []batchedActiveStopTime, in multiStopArrivalsInput, acc *arrivalsAccumulator, lookups batchedArrivalLookups) ([]statusPendingArrival, error) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	arrivals := make([]statusPendingArrival, 0, len(allActive))

	for _, b := range allActive {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		st := b.GetStopTimesForStopInWindowRow

		route, trip, reportable := lookups.resolveRouteAndTrip(st, in.RouteTypes, reqLogger)
		if !reportable {
			continue
		}

		before, after := in.Before, in.After
		if len(lookups.frequencies[st.TripID]) > 0 {
			before, after = in.FrequencyBefore, in.FrequencyAfter
		}
		if !api.stopTimeInArrivalWindow(ctx, b.activeStopTime, b.Location, b.QueryTime, before, after) {
			continue
		}

		rCopy := route
		acc.routes[route.ID] = &rCopy
		tCopy := trip
		acc.trips[trip.ID] = &tCopy

		row := arrivalInput{
			stopTime:         st,
			route:            route,
			serviceDate:      b.ServiceDate,
			location:         b.Location,
			queryTime:        b.QueryTime,
			stopCode:         b.StopCode,
			stopID:           utils.FormCombinedID(b.AgencyID, b.StopCode),
			totalStopsInTrip: lookups.tripStops.stopCounts[st.TripID],
			stopPosition:     lookups.tripStops.stopPosition(st.TripID, st.StopSequence),
			freqMap:          lookups.frequencies,
		}
		arrival, vehicle := api.buildScheduledArrival(ctx, row, acc)
		arrivals = append(arrivals, statusPendingArrival{
			arrival: *arrival,
			input:   row,
			vehicle: vehicle,
		})
	}
	return arrivals, nil
}

// stopTimeInArrivalWindow checks scheduled and available predicted times against
// the trip's window before expensive trip-status and reference construction.
// Bounds are inclusive, matching the candidate queries and Java's range check.
func (api *RestAPI) stopTimeInArrivalWindow(ctx context.Context, ast activeStopTime, location *time.Location, queryTime time.Time, before, after time.Duration) bool {
	start, end := queryTime.Add(-before), queryTime.Add(after)
	inRange := func(t time.Time) bool {
		return !t.IsZero() && !t.Before(start) && !t.After(end)
	}
	serviceStart := ast.ServiceDate.Start(location)
	arrival := serviceStart.Add(time.Duration(ast.ArrivalTime))
	departure := serviceStart.Add(time.Duration(ast.DepartureTime))
	if inRange(arrival) || inRange(departure) {
		return true
	}
	predictedArrival, predictedDeparture, predicted := api.getPredictedTimes(ctx, ast.TripID, ast.StopID, int64(ast.StopSequence), arrival, departure)
	return predicted && (inRange(predictedArrival) || inRange(predictedDeparture))
}

// convertStopsInWindowRow adapts the multi-stop window row to the single-stop
// row shape the rest of the arrivals pipeline consumes. The columns are
// identical; only the sqlc-generated Go types differ per query.
func convertStopsInWindowRow(row gtfsdb.GetStopTimesForStopsInWindowRow) gtfsdb.GetStopTimesForStopInWindowRow {
	return gtfsdb.GetStopTimesForStopInWindowRow{
		TripID:        row.TripID,
		ArrivalTime:   row.ArrivalTime,
		DepartureTime: row.DepartureTime,
		StopID:        row.StopID,
		StopSequence:  row.StopSequence,
		StopHeadsign:  row.StopHeadsign,
		RouteID:       row.RouteID,
		ServiceID:     row.ServiceID,
		TripHeadsign:  row.TripHeadsign,
		BlockID:       row.BlockID,
	}
}

// batchArrivalEntities resolves every route, trip, per-trip stop index and
// frequency row the matched stop_times need in four queries rather than per
// row. A frequency fetch failure is fatal — unlike the stop index, it cannot
// silently degrade a field, since BuildTripStatus itself needs the map.
func (api *RestAPI) batchArrivalEntities(ctx context.Context, allActiveStopTimes []activeStopTime) (
	routesLookup map[string]gtfsdb.Route,
	tripsLookup map[string]gtfsdb.Trip,
	tripStops tripStopIndex,
	freqMap map[string][]gtfsdb.Frequency,
	err error,
) {
	uniqueRouteIDs, uniqueTripIDs := uniqueRouteAndTripIDs(allActiveStopTimes)

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, uniqueRouteIDs)
	if err != nil {
		return nil, nil, tripStopIndex{}, nil, err
	}

	allTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
	if err != nil {
		return nil, nil, tripStopIndex{}, nil, err
	}

	routesLookup = make(map[string]gtfsdb.Route, len(allRoutes))
	for _, route := range allRoutes {
		routesLookup[route.ID] = route
	}

	tripsLookup = make(map[string]gtfsdb.Trip, len(allTrips))
	for _, trip := range allTrips {
		tripsLookup[trip.ID] = trip
	}

	freqMap = make(map[string][]gtfsdb.Frequency)
	if len(uniqueTripIDs) > 0 {
		freqMap, err = api.fetchFrequenciesForTrips(ctx, uniqueTripIDs)
		if err != nil {
			return nil, nil, tripStopIndex{}, nil, err
		}
	}

	return routesLookup, tripsLookup, api.buildTripStopIndex(ctx, uniqueTripIDs), freqMap, nil
}

// bestArrivalMillis is the instant Java's ArrivalAndDepartureComparator sorts
// on. A positive predicted arrival is hasPredictedArrivalTime(); otherwise
// the scheduled arrival is used.
func bestArrivalMillis(a models.ArrivalAndDeparture) int64 {
	if a.PredictedArrivalTime.UnixMilli() > 0 {
		return a.PredictedArrivalTime.UnixMilli()
	}
	return a.ScheduledArrivalTime.UnixMilli()
}

// compareBestArrival orders two arrivals by bestArrivalMillis, then by trip ID
// and stop sequence so equal times come out the same on every request.
// arrivals-and-departures-for-stop sorts on this alone. The location endpoint
// sorts by stop distance first and uses this only within one stop.
func compareBestArrival(a, b models.ArrivalAndDeparture) int {
	if c := cmp.Compare(bestArrivalMillis(a), bestArrivalMillis(b)); c != 0 {
		return c
	}
	if c := cmp.Compare(a.TripID, b.TripID); c != 0 {
		return c
	}
	return cmp.Compare(a.StopSequence, b.StopSequence)
}

// sortArrivalsByBestTime orders a single stop's arrivals with compareBestArrival.
func sortArrivalsByBestTime(arrivals []models.ArrivalAndDeparture) {
	slices.SortStableFunc(arrivals, compareBestArrival)
}

// uniqueRouteAndTripIDs collects the distinct route and trip IDs referenced by
// a set of matched stop_times.
func uniqueRouteAndTripIDs(allActiveStopTimes []activeStopTime) (routeIDs, tripIDs []string) {
	routeIDSet := make(map[string]bool)
	tripIDSet := make(map[string]bool)

	for _, ast := range allActiveStopTimes {
		st := ast.GetStopTimesForStopInWindowRow
		if st.RouteID != "" {
			routeIDSet[st.RouteID] = true
		}
		if st.TripID != "" {
			tripIDSet[st.TripID] = true
		}
	}

	return slices.Collect(maps.Keys(routeIDSet)), slices.Collect(maps.Keys(tripIDSet))
}

// tripStopKey identifies one visit of a stop on a trip by the feed's own
// stop_sequence value, which GTFS guarantees is unique within a trip.
type tripStopKey struct {
	tripID       string
	stopSequence int64
}

// tripStopIndex holds the two per-trip facts an arrival row needs from a trip's
// full ordered stop list: how many stops the trip has, and where in that list
// each stop_time falls.
type tripStopIndex struct {
	stopCounts map[string]int
	positions  map[tripStopKey]int
}

// stopPosition returns the zero-based index of a stop_time within its trip's
// ordered stop list — the "index of the stop into the sequence of stops" the
// spec defines stopSequence as. It is deliberately not the feed's raw
// stop_sequence: GTFS only requires that value to increase along the trip, so a
// 0-based or sparsely numbered feed makes the two differ.
//
// An unknown trip yields 0, matching how stopCounts degrades when the batch
// fetch below fails.
func (idx tripStopIndex) stopPosition(tripID string, stopSequence int64) int {
	return idx.positions[tripStopKey{tripID: tripID, stopSequence: stopSequence}]
}

// buildTripStopIndex resolves each trip's stop count and per-stop positions in
// one batched query rather than a pair of per-arrival lookups. A failure yields
// empty maps rather than an error: the missing values degrade two response
// fields, and the arrivals themselves are still worth returning.
func (api *RestAPI) buildTripStopIndex(ctx context.Context, tripIDs []string) tripStopIndex {
	idx := tripStopIndex{
		stopCounts: make(map[string]int, len(tripIDs)),
		positions:  make(map[tripStopKey]int),
	}
	if len(tripIDs) == 0 {
		return idx
	}
	reqLogger := logging.ForComponent(ctx, "http_server")

	// Ordered by (trip_id, stop_sequence), so each trip's running count is also
	// the next row's position within that trip.
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, tripIDs)
	if err != nil {
		reqLogger.Warn("failed to batch fetch stop times for trips", slog.Any("error", err))
		return idx
	}

	for _, st := range stopTimes {
		key := tripStopKey{tripID: st.TripID, stopSequence: st.StopSequence}
		idx.positions[key] = idx.stopCounts[st.TripID]
		idx.stopCounts[st.TripID]++
	}
	return idx
}

// arrivalInput carries the per-row values buildArrival needs. Grouped into a
// struct because several are same-typed strings and times that would be
// indistinguishable as positional arguments.
type arrivalInput struct {
	stopTime         gtfsdb.GetStopTimesForStopInWindowRow
	route            gtfsdb.Route
	serviceDate      servicedate.Date
	location         *time.Location
	queryTime        time.Time
	stopCode         string
	stopID           string
	totalStopsInTrip int
	stopPosition     int
	freqMap          map[string][]gtfsdb.Frequency
}

// buildArrival turns one matched stop_time into an ArrivalAndDeparture,
// resolving its real-time prediction and trip status along the way.
//
// The per-stop handler wants both halves immediately. The location handler
// uses buildScheduledArrival on its own so it can drop rows before the status
// half.
func (api *RestAPI) buildArrival(ctx context.Context, in arrivalInput, acc *arrivalsAccumulator) *models.ArrivalAndDeparture {
	arrival, vehicle := api.buildScheduledArrival(ctx, in, acc)
	api.attachTripStatus(ctx, arrival, in, vehicle, acc)
	return arrival
}

// buildScheduledArrival fills the schedule, prediction and vehicle fields an
// arrival needs in order to be sorted and trimmed. Trip status stays empty.
// The vehicle is returned so a later attachTripStatus does not look it up again.
func (api *RestAPI) buildScheduledArrival(ctx context.Context, in arrivalInput, acc *arrivalsAccumulator) (*models.ArrivalAndDeparture, *gtfs.Vehicle) {
	st := in.stopTime
	route := in.route
	serviceStart := in.serviceDate.Start(in.location)

	scheduledArrivalTime := serviceStart.Add(time.Duration(st.ArrivalTime))
	scheduledDepartureTime := serviceStart.Add(time.Duration(st.DepartureTime))

	// Get vehicle if available. The response's top-level `vehicleId`
	// is the combined {agencyId}_{vehicleId} form per spec, matching
	// tripStatus.vehicleId (set by BuildTripStatus below). Internal
	// lookups (GetVehicleForTrip / GetVehicleByID) use the raw RT id
	// unchanged; the combined form is an output-only concern.
	vehicle := api.GtfsManager.GetVehicleForTrip(ctx, st.TripID)
	vehicleID := api.combinedVehicleID(ctx, vehicle, route.AgencyID, st.TripID)

	// The raw feed stop_sequence, not in.stopPosition: GTFS-RT
	// stop_time_update.stop_sequence refers to the feed's own numbering, so
	// matching a prediction needs the unmodified value. The response's
	// stopSequence field is a position within the trip instead — see
	// tripStopIndex.stopPosition.
	predictedArrivalTime, predictedDepartureTime, predicted := api.getPredictedTimes(
		ctx,
		st.TripID,
		in.stopCode,
		int64(st.StopSequence),
		scheduledArrivalTime,
		scheduledDepartureTime,
	)
	if !predicted {
		predictedArrivalTime = time.Time{}
		predictedDepartureTime = time.Time{}
	}

	lastUpdateTime := api.GtfsManager.GetVehicleLastUpdateTime(vehicle)

	if acc.alertAgencyID == "" && route.AgencyID != "" {
		acc.alertAgencyID = route.AgencyID
	}

	arrival := models.NewArrivalAndDeparture(
		utils.FormCombinedID(route.AgencyID, route.ID),  // routeID
		route.ShortName.String,                          // routeShortName
		route.LongName.String,                           // routeLongName
		utils.FormCombinedID(route.AgencyID, st.TripID), // tripID
		st.TripHeadsign.String,                          // tripHeadsign
		in.stopID,                                       // stopID
		vehicleID,                                       // vehicleID
		serviceStart,                                    // serviceDate
		scheduledArrivalTime,                            // scheduledArrivalTime
		scheduledDepartureTime,                          // scheduledDepartureTime
		predictedArrivalTime,                            // predictedArrivalTime
		predictedDepartureTime,                          // predictedDepartureTime
		lastUpdateTime,                                  // lastUpdateTime
		predicted,                                       // predicted
		true,                                            // arrivalEnabled
		true,                                            // departureEnabled
		in.stopPosition,                                 // stopSequence (Zero-based position in the trip's stop list)
		in.totalStopsInTrip,                             // totalStopsInTrip
		0,                                               // numberOfStopsAway
		0,                                               // blockTripSequence
		0,                                               // distanceFromStop
		"default",                                       // status
		"",                                              // occupancyStatus
		"",                                              // predicted occupancy
		"",                                              // historical occupancy
		nil,                                             // tripStatus
		nil,                                             // situationIDs, filled by attachTripStatus
	)

	applyFrequency(arrival, in.freqMap[st.TripID], serviceStart, in.queryTime)

	return arrival, vehicle
}

// attachTripStatus fills the block-derived fields buildScheduledArrival left
// empty. BuildTripStatus already resolved this trip's situations; those are
// reused so the arrival does not repeat the alert lookup.
func (api *RestAPI) attachTripStatus(ctx context.Context, arrival *models.ArrivalAndDeparture, in arrivalInput, vehicle *gtfs.Vehicle, acc *arrivalsAccumulator) {
	tripStatus, distanceFromStop, numberOfStopsAway, situationRefs := api.tripStatusForArrival(ctx, in, vehicle, acc)

	// BuildTripStatus (via calculateBlockTripSequence) already computed
	// this and set it on the status; reuse rather than redoing the block
	// lookup for every arrival row.
	blockTripSequence := 0
	if tripStatus != nil {
		blockTripSequence = tripStatus.BlockTripSequence
	}

	arrival.TripStatus = tripStatus
	arrival.DistanceFromStop = distanceFromStop
	arrival.NumberOfStopsAway = numberOfStopsAway
	arrival.BlockTripSequence = blockTripSequence
	arrival.SituationIDs = acc.situations.addRefs(situationRefs)
}

// applyFrequency sets arrival.Frequency from the trip's frequency rows, using
// the row whose window contains queryTime. A trip with no frequency rows
// leaves Frequency nil — selectFrequency panics on an empty slice, so this
// guard is load-bearing, not defensive filler.
func applyFrequency(arrival *models.ArrivalAndDeparture, freqs []gtfsdb.Frequency, serviceStart, queryTime time.Time) {
	if len(freqs) == 0 {
		return
	}
	converted := models.NewFrequencyFromServiceStart(*selectFrequencyFromStart(freqs, serviceStart, queryTime), serviceStart)
	arrival.Frequency = &converted
}

// startTripStatusAtServiceStart reports the trip status's serviceDate and frequency from serviceStart.
func startTripStatusAtServiceStart(status *models.TripStatus, freqMap map[string][]gtfsdb.Frequency, tripID string, serviceStart, queryTime time.Time) {
	status.ServiceDate = models.NewModelTime(serviceStart)
	if status.Frequency == nil {
		return
	}
	freqs := freqMap[tripID]
	if _, activeTripID, err := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID); err == nil && len(freqMap[activeTripID]) > 0 {
		freqs = freqMap[activeTripID]
	}
	if len(freqs) == 0 {
		return
	}
	converted := models.NewFrequencyFromServiceStart(*selectFrequencyFromStart(freqs, serviceStart, queryTime), serviceStart)
	status.Frequency = &converted
}

// combinedVehicleID renders a vehicle's ID in the combined {agency}_{id} form
// the spec requires, or empty when the trip has no vehicle assigned.
func (api *RestAPI) combinedVehicleID(ctx context.Context, vehicle *gtfs.Vehicle, agencyID, tripID string) string {
	if vehicle == nil || vehicle.Trip == nil {
		return ""
	}
	reqLogger := logging.ForComponent(ctx, "http_server")
	if vehicle.ID == nil {
		reqLogger.Warn("vehicle with nil ID descriptor found for trip", "tripID", tripID)
		return ""
	}
	return utils.FormCombinedID(agencyID, vehicle.ID.ID)
}

// tripStatusForArrival builds the trip status attached to every arrival, along
// with the per-stop block metrics and situations resolved alongside it.
//
// Java attaches a BlockLocation — real-time or scheduled — to every arrival, so
// a status is expected here rather than being an optional extra.
func (api *RestAPI) tripStatusForArrival(
	ctx context.Context,
	in arrivalInput,
	vehicle *gtfs.Vehicle,
	acc *arrivalsAccumulator,
) (status *models.TripStatus, distanceFromStop float64, numberOfStopsAway int, situations []situationRef) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	st := in.stopTime

	// The vehicle is passed through rather than left nil so BuildTripStatus
	// does not repeat the GetVehicleForTrip lookup the caller already did.
	status, extras, err := api.BuildTripStatus(ctx, in.route.AgencyID, st.TripID, vehicle, in.serviceDate.Midnight(in.location), in.queryTime, in.freqMap)
	if err != nil {
		reqLogger.Warn("BuildTripStatus failed for arrival",
			"tripID", st.TripID, "error", err)
	}
	if extras != nil {
		situations = extras.situations
	}
	if status == nil {
		return nil, 0, 0, situations
	}
	startTripStatusAtServiceStart(status, in.freqMap, st.TripID, in.serviceDate.Start(in.location), in.queryTime)

	api.recordTripStatusReferences(ctx, status, st.TripID, acc)

	// Reuse the snapshot BuildTripStatus already computed for this trip.
	// BuildTripStatus applies the same schedule-deviation shift internally,
	// so recomputing here just to run metricsForStop was doubling every
	// per-arrival snapshot cost — a real problem on the plural handler
	// where minutesBefore/minutesAfter can be 24h in each direction.
	if extras != nil && extras.snapshot != nil {
		if d, n, ok := extras.snapshot.metricsForStop(st.TripID, int(st.StopSequence)); ok {
			distanceFromStop = d
			numberOfStopsAway = n
		}
	}

	return status, distanceFromStop, numberOfStopsAway, situations
}

// recordTripStatusReferences pulls the stops and the reassigned active trip that
// a trip status points at into the references accumulator, so every ID the
// arrival emits resolves in the response.
func (api *RestAPI) recordTripStatusReferences(ctx context.Context, status *models.TripStatus, scheduledTripID string, acc *arrivalsAccumulator) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	if status.NextStop != "" {
		if _, nextStopID, err := utils.ExtractAgencyIDAndCodeID(status.NextStop); err == nil {
			acc.stopIDs[nextStopID] = true
		}
	}
	if status.ClosestStop != "" {
		if _, closestStopID, err := utils.ExtractAgencyIDAndCodeID(status.ClosestStop); err == nil {
			acc.stopIDs[closestStopID] = true
		}
	}

	if status.ActiveTripID == "" {
		return
	}
	_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID)
	if err != nil || activeTripID == scheduledTripID {
		return
	}
	if _, exists := acc.trips[activeTripID]; exists {
		return
	}

	activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID)
	if err != nil {
		reqLogger.Debug("skipping active trip reference: trip not found",
			slog.String("activeTripID", activeTripID),
			slog.String("scheduledTripID", scheduledTripID),
			slog.Any("error", err))
		return
	}
	acc.trips[activeTrip.ID] = &activeTrip

	activeRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, activeTrip.RouteID)
	if err != nil {
		reqLogger.Warn("failed to fetch route for active trip reference",
			"tripID", activeTripID, "routeID", activeTrip.RouteID, "error", err)
		return
	}
	acc.routes[activeRoute.ID] = &activeRoute
}

// isRouteTypeAllowed reports whether a route survives the routeType filter. An
// empty filter accepts everything.
func isRouteTypeAllowed(routeType int64, allowed []int) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, t := range allowed {
		if int64(t) == routeType {
			return true
		}
	}
	return false
}

// arrivalsReferencesInput describes how to namespace the entities gathered in an
// arrivalsAccumulator. stopAgencies maps a bare stop ID to its owning agency;
// stops missing from it fall back to fallbackAgencyID.
type arrivalsReferencesInput struct {
	fallbackAgencyID string
	stopAgencies     map[string]string
	primaryAgency    *gtfsdb.Agency
}

// buildArrivalsReferences assembles the references block for a set of arrivals:
// their trips, the stops those arrivals and trip statuses point at, the routes
// serving them, and every route's agency.
func (api *RestAPI) buildArrivalsReferences(ctx context.Context, in arrivalsReferencesInput, acc *arrivalsAccumulator) (*models.ReferencesModel, error) {
	references := models.NewEmptyReferences()

	addedAgencyIDs := make(map[string]bool)
	if in.primaryAgency != nil {
		references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(in.primaryAgency))
		addedAgencyIDs[in.primaryAgency.ID] = true
	}

	api.appendTripReferences(ctx, references, acc)

	if err := api.appendStopReferences(ctx, references, in, acc); err != nil {
		return nil, err
	}

	api.appendRouteReferences(ctx, references, addedAgencyIDs, acc)

	return references, nil
}

func (api *RestAPI) appendTripReferences(ctx context.Context, references *models.ReferencesModel, acc *arrivalsAccumulator) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	for _, trip := range acc.trips {
		// Get the route to determine the correct agency for trip/route IDs
		route, ok := acc.routes[trip.RouteID]
		if !ok {
			fetchedRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
			if err != nil {
				reqLogger.Warn("failed to fetch route for trip reference", "tripID", trip.ID, "routeID", trip.RouteID, "error", err)
				continue // Skip instead of falling back to the stop's agency
			}
			route = &fetchedRoute
			acc.routes[trip.RouteID] = route
		}
		routeAgencyID := route.AgencyID

		tripRef := models.NewTripReference(
			utils.FormCombinedID(routeAgencyID, trip.ID),        // Use route agency for trip ID
			utils.FormCombinedID(routeAgencyID, trip.RouteID),   // Use route agency for route ID
			utils.FormCombinedID(routeAgencyID, trip.ServiceID), // Use route agency for service ID
			trip.TripHeadsign.String,
			"",
			strconv.FormatInt(trip.DirectionID.Int64, 10),
			utils.FormBlockID(routeAgencyID, trip.BlockID.String, trip.ID), // Use route agency for block ID
			utils.FormCombinedID(routeAgencyID, trip.ShapeID.String),       // Use route agency for shape ID
		)
		references.Trips = append(references.Trips, *tripRef)
	}
}

func (api *RestAPI) appendStopReferences(ctx context.Context, references *models.ReferencesModel, in arrivalsReferencesInput, acc *arrivalsAccumulator) error {
	reqLogger := logging.ForComponent(ctx, "http_server")
	stopsByID, routesByStop, err := api.loadStopReferenceData(ctx, slices.Collect(maps.Keys(acc.stopIDs)))
	if err != nil {
		return err
	}

	for stopID := range acc.stopIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		stopData, ok := stopsByID[stopID]
		if !ok {
			reqLogger.Debug("skipping stop reference: stop not found", slog.String("stopID", stopID))
			continue
		}

		combinedRouteIDs := collectStopRoutes(routesByStop[stopID], acc)

		stopAgencyID := in.fallbackAgencyID
		if agencyID, ok := in.stopAgencies[stopID]; ok && agencyID != "" {
			stopAgencyID = agencyID
		}

		references.Stops = append(references.Stops, models.Stop{
			ID:                 utils.FormCombinedID(stopAgencyID, stopData.ID),
			Name:               stopData.Name.String,
			Lat:                stopData.Lat,
			Lon:                stopData.Lon,
			Code:               nulls.StringOrNonEmpty(stopData.Code, stopData.ID),
			Parent:             parentStationID(stopAgencyID, stopData),
			Direction:          api.DirectionCalculator.CalculateStopDirection(ctx, stopData.ID, stopData.Direction),
			LocationType:       int(stopData.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stopData.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		})
	}

	added, parentRoutes, err := api.missingParentStops(ctx, references.Stops)
	if err != nil {
		reqLogger.Warn("failed to resolve parent stops", slog.Any("error", err))
		return nil
	}
	references.Stops = append(references.Stops, added...)
	collectStopRoutes(parentRoutes, acc)

	return nil
}

// loadStopReferenceData batch-fetches the stops and their routes in one shot
// instead of a query per stop.
//
// Both stop and route lookup failures are logged and degrade gracefully: a
// failed stop lookup costs the response its stop references, and a failed
// route lookup only costs each stop its routeIds. Neither is worth a 500 for
// what is otherwise a complete arrivals response.
func (api *RestAPI) loadStopReferenceData(ctx context.Context, stopIDs []string) (
	map[string]gtfsdb.Stop,
	map[string][]gtfsdb.GetRoutesForStopsRow,
	error,
) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		reqLogger.Warn("failed to batch fetch stop references", slog.Any("error", err))
		stops = nil
	}

	stopsByID := make(map[string]gtfsdb.Stop, len(stops))
	for _, s := range stops {
		stopsByID[s.ID] = s
	}

	routesByStop := make(map[string][]gtfsdb.GetRoutesForStopsRow)
	routeRows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		reqLogger.Warn("failed to batch fetch routes for stop references", slog.Any("error", err))
		return stopsByID, routesByStop, nil
	}
	for _, row := range routeRows {
		routesByStop[row.StopID] = append(routesByStop[row.StopID], row)
	}

	return stopsByID, routesByStop, nil
}

// collectStopRoutes renders one stop's routes as combined IDs, recording any
// route not already known into the accumulator so it reaches references.routes.
func collectStopRoutes(routesForStop []gtfsdb.GetRoutesForStopsRow, acc *arrivalsAccumulator) []string {
	combinedRouteIDs := make([]string, len(routesForStop))
	for i, route := range routesForStop {
		// Use route.AgencyID instead of the stop's agency
		combinedRouteIDs[i] = utils.FormCombinedID(route.AgencyID, route.ID)

		if _, exists := acc.routes[route.ID]; exists {
			continue
		}
		copied := routeFromStopRow(route)
		acc.routes[route.ID] = &copied
	}
	return combinedRouteIDs
}

func (api *RestAPI) appendRouteReferences(ctx context.Context, references *models.ReferencesModel, addedAgencyIDs map[string]bool, acc *arrivalsAccumulator) {
	reqLogger := logging.ForComponent(ctx, "http_server")
	for _, route := range acc.routes {
		references.Routes = append(references.Routes, models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String))

		// Add route agency to references if not already added
		if !addedAgencyIDs[route.AgencyID] {
			routeAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
			if err != nil {
				reqLogger.Warn("failed to fetch route agency for reference", "agencyID", route.AgencyID, "error", err)
				continue
			}
			references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&routeAgency))
			addedAgencyIDs[route.AgencyID] = true
		}
	}
}
