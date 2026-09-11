package restapi

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// stopArrivalsInput identifies one stop and the window to compute arrivals over.
// Location is the agency timezone the window is anchored in; QueryTime must
// already be expressed in it.
type stopArrivalsInput struct {
	StopCode   string
	AgencyID   string
	Location   *time.Location
	QueryTime  time.Time
	Before     time.Duration
	After      time.Duration
	RouteTypes []int // nil or empty means no route-type filter
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
	ServiceDate time.Time
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
	stopID := utils.FormCombinedID(in.AgencyID, in.StopCode)
	result := stopArrivalsResult{Arrivals: make([]models.ArrivalAndDeparture, 0)}

	allActiveStopTimes, err := api.activeStopTimesForWindow(ctx, in)
	if err != nil {
		return result, err
	}
	if len(allActiveStopTimes) == 0 {
		return result, nil
	}
	result.Matched = true

	acc.stopIDs[in.StopCode] = true

	routesLookup, tripsLookup, tripStopCountMap, freqMap, err := api.batchArrivalEntities(ctx, allActiveStopTimes)
	if err != nil {
		return result, err
	}

	for _, ast := range allActiveStopTimes {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		st := ast.GetStopTimesForStopInWindowRow
		serviceMidnight := ast.ServiceDate

		route, routeExists := routesLookup[st.RouteID]
		if !routeExists {
			api.Logger.Debug("skipping stop time: route not found in batch fetch",
				slog.String("routeID", st.RouteID),
				slog.String("tripID", st.TripID))
			continue
		}

		trip, tripExists := tripsLookup[st.TripID]
		if !tripExists {
			api.Logger.Debug("skipping stop time: trip not found in batch fetch",
				slog.String("tripID", st.TripID),
				slog.String("routeID", st.RouteID))
			continue
		}

		if !isRouteTypeAllowed(route.Type, in.RouteTypes) {
			continue
		}

		rCopy := route
		acc.routes[route.ID] = &rCopy
		tCopy := trip
		acc.trips[trip.ID] = &tCopy

		arrival := api.buildArrival(ctx, arrivalInput{
			stopTime:         st,
			route:            route,
			serviceMidnight:  serviceMidnight,
			queryTime:        in.QueryTime,
			stopCode:         in.StopCode,
			stopID:           stopID,
			totalStopsInTrip: tripStopCountMap[st.TripID],
			freqMap:          freqMap,
		}, acc)

		result.Arrivals = append(result.Arrivals, *arrival)
	}

	return result, nil
}

// activeStopTimesForWindow collects the stop_times falling inside the request
// window across yesterday, today and tomorrow, so trips whose service day
// started before midnight are not dropped.
func (api *RestAPI) activeStopTimesForWindow(ctx context.Context, in stopArrivalsInput) ([]activeStopTime, error) {
	windowStart := in.QueryTime.Add(-in.Before)
	windowEnd := in.QueryTime.Add(in.After)

	var allActiveStopTimes []activeStopTime

	for dayOffset := -1; dayOffset <= 1; dayOffset++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		dayStopTimes, err := api.stopTimesForServiceDay(ctx, in, dayOffset, windowStart, windowEnd)
		if err != nil {
			// dayOffset==0 is the user's actual service date — silently
			// dropping it would emit a 200 with the most important day's
			// arrivals missing. Fail loud for that case so clients can
			// retry. ±1-day failures stay best-effort (window-spillover only).
			if dayOffset == 0 {
				return nil, err
			}
			api.Logger.Warn("failed to resolve services for window-spillover day, skipping",
				slog.Int("day_offset", dayOffset),
				slog.Any("error", err))
			continue
		}
		allActiveStopTimes = append(allActiveStopTimes, dayStopTimes...)
	}

	return allActiveStopTimes, nil
}

// stopTimesForServiceDay returns the stop_times of a single service day that
// fall inside the window, keeping only those whose service is active that day.
//
// The two failure modes are deliberately different: not being able to resolve
// the day's active services is returned to the caller, which decides whether
// that day is essential, while an unreadable stop_times page is logged and
// yields no rows.
func (api *RestAPI) stopTimesForServiceDay(
	ctx context.Context,
	in stopArrivalsInput,
	dayOffset int,
	windowStart, windowEnd time.Time,
) ([]activeStopTime, error) {
	targetDate := in.QueryTime.AddDate(0, 0, dayOffset)
	serviceMidnight := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, in.Location)
	serviceDateStr := targetDate.Format("20060102")

	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDateStr)
	if err != nil {
		return nil, fmt.Errorf("query active service IDs for %s: %w", serviceDateStr, err)
	}
	if len(activeServiceIDs) == 0 {
		return nil, nil
	}

	endOffset := windowEnd.Sub(serviceMidnight)
	if endOffset < 0 {
		return nil, nil
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForStopInWindow(ctx, gtfsdb.GetStopTimesForStopInWindowParams{
		StopID:           in.StopCode,
		WindowStartNanos: windowStart.Sub(serviceMidnight).Nanoseconds(),
		WindowEndNanos:   endOffset.Nanoseconds(),
	})
	if err != nil {
		api.Logger.Warn("failed to query stop times in window",
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
		if activeServiceIDSet[st.ServiceID] {
			dayStopTimes = append(dayStopTimes, activeStopTime{
				GetStopTimesForStopInWindowRow: st,
				ServiceDate:                    serviceMidnight,
			})
		}
	}
	return dayStopTimes, nil
}

// batchArrivalEntities resolves every route, trip, per-trip stop count and
// frequency row the matched stop_times need in four queries rather than per
// row. A frequency fetch failure is fatal — unlike stop count, it cannot
// silently degrade a field, since BuildTripStatus itself needs the map.
func (api *RestAPI) batchArrivalEntities(ctx context.Context, allActiveStopTimes []activeStopTime) (
	routesLookup map[string]gtfsdb.Route,
	tripsLookup map[string]gtfsdb.Trip,
	tripStopCountMap map[string]int,
	freqMap map[string][]gtfsdb.Frequency,
	err error,
) {
	uniqueRouteIDs, uniqueTripIDs := uniqueRouteAndTripIDs(allActiveStopTimes)

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, uniqueRouteIDs)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	allTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
	if err != nil {
		return nil, nil, nil, nil, err
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
			return nil, nil, nil, nil, err
		}
	}

	return routesLookup, tripsLookup, api.tripStopCounts(ctx, uniqueTripIDs), freqMap, nil
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

// tripStopCounts returns how many stops each trip has, batched to avoid a
// per-arrival query for totalStopsInTrip. A failure yields an empty map rather
// than an error: a missing count degrades one response field, and the arrivals
// themselves are still worth returning.
func (api *RestAPI) tripStopCounts(ctx context.Context, tripIDs []string) map[string]int {
	counts := make(map[string]int, len(tripIDs))
	if len(tripIDs) == 0 {
		return counts
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, tripIDs)
	if err != nil {
		api.Logger.Warn("failed to batch fetch stop times for trips", slog.Any("error", err))
		return counts
	}

	for _, st := range stopTimes {
		counts[st.TripID]++
	}
	return counts
}

// arrivalInput carries the per-row values buildArrival needs. Grouped into a
// struct because several are same-typed strings and times that would be
// indistinguishable as positional arguments.
type arrivalInput struct {
	stopTime         gtfsdb.GetStopTimesForStopInWindowRow
	route            gtfsdb.Route
	serviceMidnight  time.Time
	queryTime        time.Time
	stopCode         string
	stopID           string
	totalStopsInTrip int
	freqMap          map[string][]gtfsdb.Frequency
}

// buildArrival turns one matched stop_time into an ArrivalAndDeparture,
// resolving its real-time prediction and trip status along the way.
func (api *RestAPI) buildArrival(ctx context.Context, in arrivalInput, acc *arrivalsAccumulator) *models.ArrivalAndDeparture {
	st := in.stopTime
	route := in.route

	scheduledArrivalTime := in.serviceMidnight.Add(time.Duration(st.ArrivalTime))
	scheduledDepartureTime := in.serviceMidnight.Add(time.Duration(st.DepartureTime))

	// Get vehicle if available. The response's top-level `vehicleId`
	// is the combined {agencyId}_{vehicleId} form per spec, matching
	// tripStatus.vehicleId (set by BuildTripStatus below). Internal
	// lookups (GetVehicleForTrip / GetVehicleByID) use the raw RT id
	// unchanged; the combined form is an output-only concern.
	vehicle := api.GtfsManager.GetVehicleForTrip(ctx, st.TripID)
	vehicleID := api.combinedVehicleID(vehicle, route.AgencyID, st.TripID)

	predictedArrivalTime, predictedDepartureTime, predicted := api.getPredictedTimes(
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

	tripStatus, distanceFromStop, numberOfStopsAway, situationRefs := api.tripStatusForArrival(ctx, in, vehicle, acc)

	// BuildTripStatus (via calculateBlockTripSequence) already computed
	// this and set it on the status; reuse rather than redoing the block
	// lookup for every arrival row.
	blockTripSequence := 0
	if tripStatus != nil {
		blockTripSequence = tripStatus.BlockTripSequence
	}

	lastUpdateTime := api.GtfsManager.GetVehicleLastUpdateTime(vehicle)

	// BuildTripStatus already resolved this trip's situations. Reuse those
	// references so each arrival does not repeat the alert lookup and its
	// situationIds are guaranteed to match references.situations.
	situationIDs := acc.situations.addRefs(situationRefs)

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
		in.serviceMidnight,                              // serviceDate
		scheduledArrivalTime,                            // scheduledArrivalTime
		scheduledDepartureTime,                          // scheduledDepartureTime
		predictedArrivalTime,                            // predictedArrivalTime
		predictedDepartureTime,                          // predictedDepartureTime
		lastUpdateTime,                                  // lastUpdateTime
		predicted,                                       // predicted
		true,                                            // arrivalEnabled
		true,                                            // departureEnabled
		int(st.StopSequence)-1,                          // stopSequence (Zero-based index)
		in.totalStopsInTrip,                             // totalStopsInTrip
		numberOfStopsAway,                               // numberOfStopsAway
		blockTripSequence,                               // blockTripSequence
		distanceFromStop,                                // distanceFromStop
		"default",                                       // status
		"",                                              // occupancyStatus
		"",                                              // predicted occupancy
		"",                                              // historical occupancy
		tripStatus,                                      // tripStatus
		situationIDs,                                    // situationIDs
	)

	applyFrequency(arrival, in.freqMap[st.TripID], in.serviceMidnight, in.queryTime)

	return arrival
}

// applyFrequency sets arrival.Frequency from the trip's frequency rows, using
// the row whose window contains queryTime. A trip with no frequency rows
// leaves Frequency nil — selectFrequency panics on an empty slice, so this
// guard is load-bearing, not defensive filler.
func applyFrequency(arrival *models.ArrivalAndDeparture, freqs []gtfsdb.Frequency, serviceMidnight, queryTime time.Time) {
	if len(freqs) == 0 {
		return
	}
	converted := models.NewFrequencyFromDB(*selectFrequency(freqs, serviceMidnight, queryTime), serviceMidnight)
	arrival.Frequency = &converted
}

// combinedVehicleID renders a vehicle's ID in the combined {agency}_{id} form
// the spec requires, or empty when the trip has no vehicle assigned.
func (api *RestAPI) combinedVehicleID(vehicle *gtfs.Vehicle, agencyID, tripID string) string {
	if vehicle == nil || vehicle.Trip == nil {
		return ""
	}
	if vehicle.ID == nil {
		api.Logger.Warn("vehicle with nil ID descriptor found for trip", "tripID", tripID)
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
	st := in.stopTime

	// The vehicle is passed through rather than left nil so BuildTripStatus
	// does not repeat the GetVehicleForTrip lookup the caller already did.
	status, extras, err := api.BuildTripStatus(ctx, in.route.AgencyID, st.TripID, vehicle, in.serviceMidnight, in.queryTime, in.freqMap)
	if err != nil {
		api.Logger.Warn("BuildTripStatus failed for arrival",
			"tripID", st.TripID, "error", err)
	}
	if extras != nil {
		situations = extras.situations
	}
	if status == nil {
		return nil, 0, 0, situations
	}

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
		api.Logger.Debug("skipping active trip reference: trip not found",
			slog.String("activeTripID", activeTripID),
			slog.String("scheduledTripID", scheduledTripID),
			slog.Any("error", err))
		return
	}
	acc.trips[activeTrip.ID] = &activeTrip

	activeRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, activeTrip.RouteID)
	if err != nil {
		api.Logger.Warn("failed to fetch route for active trip reference",
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
	for _, trip := range acc.trips {
		// Get the route to determine the correct agency for trip/route IDs
		route, ok := acc.routes[trip.RouteID]
		if !ok {
			fetchedRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
			if err != nil {
				api.Logger.Warn("failed to fetch route for trip reference", "tripID", trip.ID, "routeID", trip.RouteID, "error", err)
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
			utils.FormCombinedID(routeAgencyID, trip.BlockID.String), // Use route agency for block ID
			utils.FormCombinedID(routeAgencyID, trip.ShapeID.String), // Use route agency for shape ID
		)
		references.Trips = append(references.Trips, *tripRef)
	}
}

func (api *RestAPI) appendStopReferences(ctx context.Context, references *models.ReferencesModel, in arrivalsReferencesInput, acc *arrivalsAccumulator) error {
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
			api.Logger.Debug("skipping stop reference: stop not found", slog.String("stopID", stopID))
			continue
		}

		combinedRouteIDs := collectStopRoutes(routesByStop[stopID], acc)

		stopAgencyID := in.fallbackAgencyID
		if agencyID, ok := in.stopAgencies[stopID]; ok && agencyID != "" {
			stopAgencyID = agencyID
		}

		// NOTE: deliberately not buildStopModel — that helper defaults Code to
		// the stop ID when stops.code is NULL, which would change this
		// endpoint's existing output.
		references.Stops = append(references.Stops, models.Stop{
			ID:                 utils.FormCombinedID(stopAgencyID, stopData.ID),
			Name:               stopData.Name.String,
			Lat:                stopData.Lat,
			Lon:                stopData.Lon,
			Code:               stopData.Code.String,
			Direction:          api.DirectionCalculator.CalculateStopDirection(ctx, stopData.ID, stopData.Direction),
			LocationType:       int(stopData.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stopData.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		})
	}

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
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		api.Logger.Warn("failed to batch fetch stop references", slog.Any("error", err))
		stops = nil
	}

	stopsByID := make(map[string]gtfsdb.Stop, len(stops))
	for _, s := range stops {
		stopsByID[s.ID] = s
	}

	routesByStop := make(map[string][]gtfsdb.GetRoutesForStopsRow)
	routeRows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		api.Logger.Warn("failed to batch fetch routes for stop references", slog.Any("error", err))
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
		acc.routes[route.ID] = &gtfsdb.Route{
			ID:        route.ID,
			AgencyID:  route.AgencyID,
			ShortName: route.ShortName,
			LongName:  route.LongName,
			Desc:      route.Desc,
			Type:      route.Type,
			Url:       route.Url,
			Color:     route.Color,
			TextColor: route.TextColor,
		}
	}
	return combinedRouteIDs
}

func (api *RestAPI) appendRouteReferences(ctx context.Context, references *models.ReferencesModel, addedAgencyIDs map[string]bool, acc *arrivalsAccumulator) {
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
				api.Logger.Warn("failed to fetch route agency for reference", "agencyID", route.AgencyID, "error", err)
				continue
			}
			references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&routeAgency))
			addedAgencyIDs[route.AgencyID] = true
		}
	}
}
