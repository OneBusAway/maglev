package restapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// ArrivalsAndDeparturesForLocationParams holds all parsed and validated query
// parameters for the arrivals-and-departures-for-location endpoint.
type ArrivalsAndDeparturesForLocationParams struct {
	Lat     float64
	Lon     float64
	Radius  float64
	LatSpan float64
	LonSpan float64

	Time                   time.Time
	MinutesBefore          int
	MinutesAfter           int
	FrequencyMinutesBefore int
	FrequencyMinutesAfter  int

	MaxCount             int
	EmptyReturnsNotFound bool
	RouteTypes           []int
}

// activeStopTime pairs a GTFS stop time with the service date it occurs on.
type activeStopTime struct {
	gtfsdb.GetStopTimesForStopInWindowRow
	ServiceDate time.Time
}

// locationArrivalsState holds the shared accumulation state across all stops
// while processing arrivals and departures for a location.
type locationArrivalsState struct {
	arrivals           []models.ArrivalAndDeparture
	tripIDSet          map[string]*gtfsdb.Trip
	routeIDSet         map[string]*gtfsdb.Route
	stopIDSet          map[string]bool
	stopAgencyOverride map[string]string
	stopsWithArrivals  map[string]bool
	collectedAlerts    map[string]gtfs.Alert
	limitExceeded      bool

	stopAgencyMap    map[string]string
	fallbackAgencyID string
	agencyLoc        *time.Location
}

func newLocationArrivalsState() *locationArrivalsState {
	return &locationArrivalsState{
		arrivals:           make([]models.ArrivalAndDeparture, 0),
		tripIDSet:          make(map[string]*gtfsdb.Trip),
		routeIDSet:         make(map[string]*gtfsdb.Route),
		stopIDSet:          make(map[string]bool),
		stopAgencyOverride: make(map[string]string),
		stopsWithArrivals:  make(map[string]bool),
		collectedAlerts:    make(map[string]gtfs.Alert),
	}
}

// Error message constants shared by the parameter-parsing helpers below.
const (
	errMustBeValidInteger       = "must be a valid integer"
	errMustBeNonNegativeInteger = "must be a non-negative integer"
)

// parseArrivalsAndDeparturesForLocationParams parses and validates all query
// parameters for this endpoint in one place.
func (api *RestAPI) parseArrivalsAndDeparturesForLocationParams(r *http.Request) (ArrivalsAndDeparturesForLocationParams, map[string][]string) {
	const (
		defaultMinutesBefore = 5
		defaultMinutesAfter  = 35
		maxMinutesBefore     = 60
		maxMinutesAfter      = 240
		defaultMaxCount      = 250
	)

	params := ArrivalsAndDeparturesForLocationParams{
		Time:          api.Clock.Now(),
		MinutesBefore: defaultMinutesBefore,
		MinutesAfter:  defaultMinutesAfter,
		MaxCount:      defaultMaxCount,
	}

	var fieldErrors map[string][]string
	addError := func(field, msg string) {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		fieldErrors[field] = append(fieldErrors[field], msg)
	}

	// Spatial params (required) — reuse the shared location parser.
	loc, locErrors := api.parseLocationParams(r, nil)
	if len(locErrors) > 0 {
		mergeFieldErrors(&fieldErrors, locErrors)
	} else {
		params.Lat = loc.Lat
		params.Lon = loc.Lon
		params.Radius = loc.Radius
		params.LatSpan = loc.LatSpan
		params.LonSpan = loc.LonSpan
	}

	q := r.URL.Query()
	params.Time = parseTimeParam(q, params.Time, addError)
	parseMinutesCappedParam(q, "minutesBefore", maxMinutesBefore, &params.MinutesBefore, addError)
	parseMinutesCappedParam(q, "minutesAfter", maxMinutesAfter, &params.MinutesAfter, addError)
	parseMinutesUncappedParam(q, "frequencyMinutesBefore", &params.FrequencyMinutesBefore, addError)
	parseMinutesUncappedParam(q, "frequencyMinutesAfter", &params.FrequencyMinutesAfter, addError)
	params.EmptyReturnsNotFound = parseEmptyReturnsNotFoundParam(q, addError)
	params.RouteTypes = parseRouteTypesParam(q, addError)

	var maxCountErrors map[string][]string
	params.MaxCount, maxCountErrors = utils.ParseMaxCount(q, defaultMaxCount, nil)
	mergeFieldErrors(&fieldErrors, maxCountErrors)

	return params, fieldErrors
}

// parseTimeParam parses the "time" query parameter as a Unix timestamp in
// milliseconds. Returns defaultTime unchanged when the parameter is absent.
func parseTimeParam(q url.Values, defaultTime time.Time, addError func(string, string)) time.Time {
	val := q.Get("time")
	if val == "" {
		return defaultTime
	}
	ms, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		addError("time", "must be a valid Unix timestamp in milliseconds")
		return defaultTime
	}
	return time.Unix(ms/1000, (ms%1000)*1_000_000)
}

// parseMinutesCappedParam parses an integer minutes query parameter and writes
// the result into dest. Values above maxVal are silently capped; negative
// values and non-integer values are rejected via addError.
func parseMinutesCappedParam(q url.Values, key string, maxVal int, dest *int, addError func(string, string)) {
	val := q.Get(key)
	if val == "" {
		return
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		addError(key, errMustBeValidInteger)
		return
	}
	if n < 0 {
		addError(key, errMustBeNonNegativeInteger)
		return
	}
	if n > maxVal {
		*dest = maxVal
		return
	}
	*dest = n
}

// parseMinutesUncappedParam parses an integer minutes query parameter with no
// upper bound and writes the result into dest.
func parseMinutesUncappedParam(q url.Values, key string, dest *int, addError func(string, string)) {
	val := q.Get(key)
	if val == "" {
		return
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		addError(key, errMustBeValidInteger)
		return
	}
	if n < 0 {
		addError(key, errMustBeNonNegativeInteger)
		return
	}
	*dest = n
}

// parseEmptyReturnsNotFoundParam parses the "emptyReturnsNotFound" boolean
// query parameter. Returns false when absent or invalid.
func parseEmptyReturnsNotFoundParam(q url.Values, addError func(string, string)) bool {
	val := q.Get("emptyReturnsNotFound")
	if val == "" {
		return false
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		addError("emptyReturnsNotFound", "must be true or false")
		return false
	}
	return b
}

// parseRouteTypesParam parses the "routeType" comma-delimited integer list
// query parameter. Returns nil when absent; stops and errors at the first
// invalid token.
func parseRouteTypesParam(q url.Values, addError func(string, string)) []int {
	val := q.Get("routeType")
	if val == "" {
		return nil
	}
	var routeTypes []int
	for _, p := range strings.Split(val, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		rt, err := strconv.Atoi(p)
		if err != nil {
			addError("routeType", "must be a comma-delimited list of integers")
			return nil
		}
		routeTypes = append(routeTypes, rt)
	}
	return routeTypes
}

// mergeFieldErrors merges src into *dst, initialising *dst lazily if nil.
func mergeFieldErrors(dst *map[string][]string, src map[string][]string) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string][]string)
	}
	for k, v := range src {
		(*dst)[k] = append((*dst)[k], v...)
	}
}

// arrivalStatusFromDeviation derives a human-readable status string from a
// schedule deviation, matching Java's ArrivalAndDepartureBeanServiceImpl logic.
//
//   - deviation > 300s  (5+ min late)  → "LATE"
//   - deviation < -180s (3+ min early) → "EARLY"
//   - otherwise                        → "ON_TIME"
//
// When there is no real-time data the caller should pass "default" directly.
func arrivalStatusFromDeviation(deviationSeconds int) string {
	switch {
	case deviationSeconds > 300:
		return "LATE"
	case deviationSeconds < -180:
		return "EARLY"
	default:
		return "ON_TIME"
	}
}

// arrivalsAndDeparturesForLocationHandler returns arrivals and departures for all
// stops within a geographic bounding box (lat/lon + latSpan/lonSpan or radius).
//
// Java equivalent: ArrivalsAndDeparturesForLocationAction.index()
func (api *RestAPI) arrivalsAndDeparturesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params, fieldErrors := api.parseArrivalsAndDeparturesForLocationParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	stops, _ := api.GtfsManager.GetStopsForLocation(
		ctx,
		&internalgtfs.LocationParams{
			Lat:     params.Lat,
			Lon:     params.Lon,
			Radius:  params.Radius,
			LatSpan: params.LatSpan,
			LonSpan: params.LonSpan,
		},
		"",
		params.MaxCount,
		params.RouteTypes,
	)

	if len(stops) == 0 {
		api.handleEmptyStopsResponseForLocation(w, r, params)
		return
	}

	state := newLocationArrivalsState()
	if err := api.resolveAgenciesForStopsLocation(ctx, stops, state); err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Fan out: collect arrivals across every stop in the bbox.
	for _, dbStop := range stops {
		if state.limitExceeded || len(state.arrivals) >= params.MaxCount {
			state.limitExceeded = true
			break
		}
		if err := api.collectArrivalsForLocationStop(ctx, w, r, dbStop, params, state); err != nil {
			return // Context cancellation/error response already handled.
		}
	}

	api.sortLocationArrivalsByTime(state.arrivals)

	// Collect stop-level service alerts.
	rawStopCodes := make([]string, 0, len(stops))
	for _, s := range stops {
		rawStopCodes = append(rawStopCodes, s.ID)
	}
	for _, sc := range rawStopCodes {
		for _, alert := range api.GtfsManager.GetAlertsForStop(sc) {
			if alert.ID != "" {
				if _, seen := state.collectedAlerts[alert.ID]; !seen {
					state.collectedAlerts[alert.ID] = alert
				}
			}
		}
	}

	references, topLevelSituationIDs := api.buildLocationReferencesBlock(ctx, state)
	queriedStopIDs := api.buildLocationQueriedStopIDs(stops, state)
	nearbyStops := getLocationNearbyStops(api, ctx, params.Lat, params.Lon, params.Time)

	api.sendResponse(w, r, models.NewArrivalsAndDeparturesForLocationResponse(
		state.arrivals,
		*references,
		nearbyStops,
		topLevelSituationIDs,
		queriedStopIDs,
		state.limitExceeded,
		api.Clock,
	))
}

func (api *RestAPI) handleEmptyStopsResponseForLocation(w http.ResponseWriter, r *http.Request, params ArrivalsAndDeparturesForLocationParams) {
	if params.EmptyReturnsNotFound {
		api.sendNotFound(w, r)
		return
	}
	api.sendResponse(w, r, models.NewArrivalsAndDeparturesForLocationResponse(
		[]models.ArrivalAndDeparture{},
		*models.NewEmptyReferences(),
		[]models.StopWithDistance{},
		[]string{},
		[]string{},
		false,
		api.Clock,
	))
}

func (api *RestAPI) resolveAgenciesForStopsLocation(ctx context.Context, stops []gtfsdb.Stop, state *locationArrivalsState) error {
	rawStopCodes := make([]string, 0, len(stops))
	for _, s := range stops {
		rawStopCodes = append(rawStopCodes, s.ID)
	}

	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, rawStopCodes)
	if err != nil {
		return err
	}

	state.stopAgencyMap = make(map[string]string, len(agencyRows))
	for _, row := range agencyRows {
		if _, exists := state.stopAgencyMap[row.StopID]; !exists {
			state.stopAgencyMap[row.StopID] = row.ID
		}
	}

	state.fallbackAgencyID = pickPrimaryAgency(state.stopAgencyMap)
	state.agencyLoc = time.UTC
	if state.fallbackAgencyID != "" {
		if ag, tzErr := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, state.fallbackAgencyID); tzErr == nil {
			if parsed, parseErr := loadAgencyLocation(ag.ID, ag.Timezone); parseErr == nil {
				state.agencyLoc = parsed
			}
		}
	}
	return nil
}

func (api *RestAPI) collectArrivalsForLocationStop(ctx context.Context, w http.ResponseWriter, r *http.Request, dbStop gtfsdb.Stop, params ArrivalsAndDeparturesForLocationParams, state *locationArrivalsState) error {
	stopCode := dbStop.ID
	agencyID := state.stopAgencyMap[stopCode]
	if agencyID == "" {
		agencyID = state.fallbackAgencyID
	}
	state.stopIDSet[stopCode] = true

	stopLoc := state.agencyLoc
	if agencyID != state.fallbackAgencyID {
		if ag, agErr := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID); agErr == nil {
			if parsed, parseErr := loadAgencyLocation(ag.ID, ag.Timezone); parseErr == nil {
				stopLoc = parsed
			}
		}
	}

	stopQueryTime := params.Time.In(stopLoc)
	allActiveStopTimes, err := api.fetchActiveStopTimesForLocationWindow(ctx, stopCode, stopLoc, stopQueryTime, params)
	if err != nil {
		api.clientCanceledResponse(w, r, err)
		return err
	}
	if len(allActiveStopTimes) == 0 {
		return nil
	}

	stopProducedArrival, err := api.buildArrivalsFromLocationStopTimes(ctx, w, r, stopCode, agencyID, allActiveStopTimes, params, stopQueryTime, state)
	if err != nil {
		return err
	}
	if stopProducedArrival {
		state.stopsWithArrivals[stopCode] = true
	}
	return nil
}

func (api *RestAPI) fetchActiveStopTimesForLocationWindow(ctx context.Context, stopCode string, stopLoc *time.Location, stopQueryTime time.Time, params ArrivalsAndDeparturesForLocationParams) ([]activeStopTime, error) {
	stopWindowStart := stopQueryTime.Add(-time.Duration(params.MinutesBefore) * time.Minute)
	stopWindowEnd := stopQueryTime.Add(time.Duration(params.MinutesAfter) * time.Minute)

	var allActiveStopTimes []activeStopTime
	for dayOffset := -1; dayOffset <= 1; dayOffset++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		targetDate := stopQueryTime.AddDate(0, 0, dayOffset)
		serviceMidnight := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, stopLoc)
		serviceDateStr := targetDate.Format("20060102")

		activeServiceIDs, svcErr := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDateStr)
		if svcErr != nil {
			api.Logger.Warn("failed to query active service IDs", slog.String("date", serviceDateStr), slog.Any("error", svcErr))
			continue
		}
		if len(activeServiceIDs) == 0 {
			continue
		}

		activeServiceIDSet := make(map[string]bool, len(activeServiceIDs))
		for _, sid := range activeServiceIDs {
			activeServiceIDSet[sid] = true
		}

		startNanos := stopWindowStart.Sub(serviceMidnight).Nanoseconds()
		endNanos := stopWindowEnd.Sub(serviceMidnight).Nanoseconds()
		if endNanos < 0 {
			continue
		}

		stopTimes, stErr := api.GtfsManager.GtfsDB.Queries.GetStopTimesForStopInWindow(ctx, gtfsdb.GetStopTimesForStopInWindowParams{
			StopID:           stopCode,
			WindowStartNanos: startNanos,
			WindowEndNanos:   endNanos,
		})
		if stErr != nil {
			api.Logger.Warn("failed to query stop times in window", slog.String("stopID", stopCode), slog.Any("error", stErr))
			continue
		}

		for _, st := range stopTimes {
			if activeServiceIDSet[st.ServiceID] {
				allActiveStopTimes = append(allActiveStopTimes, activeStopTime{
					GetStopTimesForStopInWindowRow: st,
					ServiceDate:                    serviceMidnight,
				})
			}
		}
	}
	return allActiveStopTimes, nil
}

func (api *RestAPI) buildArrivalsFromLocationStopTimes(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	stopCode string,
	agencyID string,
	allActiveStopTimes []activeStopTime,
	params ArrivalsAndDeparturesForLocationParams,
	stopQueryTime time.Time,
	state *locationArrivalsState,
) (bool, error) {
	batchRouteIDs := make(map[string]bool)
	batchTripIDs := make(map[string]bool)
	for _, ast := range allActiveStopTimes {
		if ast.RouteID != "" {
			batchRouteIDs[ast.RouteID] = true
		}
		if ast.TripID != "" {
			batchTripIDs[ast.TripID] = true
		}
	}

	uniqueRouteIDs := stringMapKeys(batchRouteIDs)
	uniqueTripIDs := stringMapKeys(batchTripIDs)

	fetchedRoutes, rErr := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, uniqueRouteIDs)
	if rErr != nil {
		api.Logger.Warn("failed to batch fetch routes", slog.String("stopID", stopCode), slog.Any("error", rErr))
		return false, nil
	}
	fetchedTrips, tErr := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
	if tErr != nil {
		api.Logger.Warn("failed to batch fetch trips", slog.String("stopID", stopCode), slog.Any("error", tErr))
		return false, nil
	}

	routesLookup := make(map[string]gtfsdb.Route, len(fetchedRoutes))
	for _, rt := range fetchedRoutes {
		routesLookup[rt.ID] = rt
	}
	tripsLookup := make(map[string]gtfsdb.Trip, len(fetchedTrips))
	for _, tr := range fetchedTrips {
		tripsLookup[tr.ID] = tr
	}

	tripStopCountMap := make(map[string]int, len(uniqueTripIDs))
	if len(uniqueTripIDs) > 0 {
		allST, countErr := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, uniqueTripIDs)
		if countErr != nil {
			api.Logger.Warn("failed to batch fetch stop times for trips", slog.Any("error", countErr))
		} else {
			for _, st := range allST {
				tripStopCountMap[st.TripID]++
			}
		}
	}

	stopProducedArrival := false
	combinedStopID := utils.FormCombinedID(agencyID, stopCode)

	for _, ast := range allActiveStopTimes {
		if len(state.arrivals) >= params.MaxCount {
			state.limitExceeded = true
			break
		}
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return stopProducedArrival, ctx.Err()
		}

		st := ast.GetStopTimesForStopInWindowRow
		serviceMidnight := ast.ServiceDate

		route, routeOK := routesLookup[st.RouteID]
		if !routeOK {
			continue
		}
		trip, tripOK := tripsLookup[st.TripID]
		if !tripOK {
			continue
		}

		rCopy := route
		state.routeIDSet[route.ID] = &rCopy
		tCopy := trip
		state.tripIDSet[trip.ID] = &tCopy

		scheduledArrivalTime := serviceMidnight.Add(time.Duration(st.ArrivalTime))
		scheduledDepartureTime := serviceMidnight.Add(time.Duration(st.DepartureTime))

		var (
			predictedArrivalTime   time.Time
			predictedDepartureTime time.Time
			predicted              = false
			vehicleID              string
			tripStatus             *models.TripStatus
			distanceFromStop       = 0.0
			numberOfStopsAway      = 0
			lastUpdateTime         time.Time
			arrivalStatus          = "default"
		)

		vehicle := api.GtfsManager.GetVehicleForTrip(ctx, st.TripID)
		if vehicle != nil && vehicle.Trip != nil && vehicle.ID != nil {
			vehicleID = vehicle.ID.ID
		}

		predArr, predDep, isPredicted := api.getPredictedTimes(
			st.TripID, stopCode, int64(st.StopSequence),
			scheduledArrivalTime, scheduledDepartureTime,
		)
		if isPredicted {
			predicted = true
			predictedArrivalTime = predArr
			predictedDepartureTime = predDep
		}

		if vehicle != nil {
			status, statusErr := api.BuildTripStatus(ctx, route.AgencyID, st.TripID, vehicle, serviceMidnight, stopQueryTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "tripID", st.TripID, "error", statusErr)
			}
			if status != nil {
				tripStatus = status

				if !predicted && status.Predicted {
					dev := time.Duration(status.ScheduleDeviation) * time.Second
					predictedArrivalTime = scheduledArrivalTime.Add(dev)
					predictedDepartureTime = scheduledDepartureTime.Add(dev)
					predicted = true
				}

				if predicted {
					arrivalStatus = arrivalStatusFromDeviation(status.ScheduleDeviation)
				}

				if status.NextStop != "" {
					if nsAgency, nsID, nsErr := utils.ExtractAgencyIDAndCodeID(status.NextStop); nsErr == nil {
						state.stopIDSet[nsID] = true
						if nsAgency != "" {
							state.stopAgencyOverride[nsID] = nsAgency
						}
					}
				}
				if status.ClosestStop != "" {
					if csAgency, csID, csErr := utils.ExtractAgencyIDAndCodeID(status.ClosestStop); csErr == nil {
						state.stopIDSet[csID] = true
						if csAgency != "" {
							state.stopAgencyOverride[csID] = csAgency
						}
					}
				}

				if vehicle.Position != nil {
					distanceFromStop = api.getBlockDistanceToStop(ctx, st.TripID, stopCode, vehicle, stopQueryTime)
					nsa := api.getNumberOfStopsAway(ctx, st.TripID, int(st.StopSequence), vehicle, stopQueryTime)
					if nsa != nil {
						numberOfStopsAway = *nsa
					} else {
						numberOfStopsAway = -1
					}
				}

				if status.ActiveTripID != "" {
					if _, atID, atErr := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID); atErr == nil {
						if activeSeq := api.calculateBlockTripSequence(ctx, atID, serviceMidnight); activeSeq > 0 {
							status.BlockTripSequence = activeSeq
						}

						if atID != st.TripID {
							if _, exists := state.tripIDSet[atID]; !exists {
								if at, atFetchErr := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, atID); atFetchErr == nil {
									atCopy := at
									state.tripIDSet[at.ID] = &atCopy
									if ar, arFetchErr := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, at.RouteID); arFetchErr == nil {
										arCopy := ar
										state.routeIDSet[ar.ID] = &arCopy
									}
								}
							}
						}
					}
				}
			}
			lastUpdateTime = api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
		}

		totalStopsInTrip := tripStopCountMap[st.TripID]
		blockTripSequence := api.calculateBlockTripSequence(ctx, st.TripID, serviceMidnight)

		tripAlerts := api.GtfsManager.GetAlertsForTrip(ctx, st.TripID)
		situationIDs := make([]string, 0, len(tripAlerts))
		for _, alert := range tripAlerts {
			if alert.ID == "" {
				continue
			}
			situationIDs = append(situationIDs, alert.ID)
			if _, seen := state.collectedAlerts[alert.ID]; !seen {
				state.collectedAlerts[alert.ID] = alert
			}
		}

		formattedVehicleID := ""
		if vehicleID != "" {
			formattedVehicleID = utils.FormCombinedID(route.AgencyID, vehicleID)
		}

		rawStopSequence := int(st.StopSequence) - 1

		state.arrivals = append(state.arrivals, *models.NewArrivalAndDeparture(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.ShortName.String,
			route.LongName.String,
			utils.FormCombinedID(route.AgencyID, st.TripID),
			st.TripHeadsign.String,
			combinedStopID,
			formattedVehicleID,
			serviceMidnight,
			scheduledArrivalTime,
			scheduledDepartureTime,
			predictedArrivalTime,
			predictedDepartureTime,
			lastUpdateTime,
			predicted,
			true,
			true,
			rawStopSequence,
			totalStopsInTrip,
			numberOfStopsAway,
			blockTripSequence,
			distanceFromStop,
			arrivalStatus,
			"", "", "",
			tripStatus,
			situationIDs,
		))
		stopProducedArrival = true
	}

	return stopProducedArrival, nil
}

func (api *RestAPI) sortLocationArrivalsByTime(arrivals []models.ArrivalAndDeparture) {
	sort.Slice(arrivals, func(i, j int) bool {
		ai := arrivals[i]
		aj := arrivals[j]
		var ti, tj time.Time
		if !ai.PredictedArrivalTime.IsZero() {
			ti = ai.PredictedArrivalTime.Time
		} else {
			ti = ai.ScheduledArrivalTime.Time
		}
		if !aj.PredictedArrivalTime.IsZero() {
			tj = aj.PredictedArrivalTime.Time
		} else {
			tj = aj.ScheduledArrivalTime.Time
		}
		return ti.Before(tj)
	})
}

func (api *RestAPI) buildLocationReferencesBlock(ctx context.Context, state *locationArrivalsState) (*models.ReferencesModel, []string) {
	references := models.NewEmptyReferences()
	addedAgencyIDs := make(map[string]bool)

	for _, trip := range state.tripIDSet {
		routeForTrip, ok := state.routeIDSet[trip.RouteID]
		if !ok {
			if fetched, fErr := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID); fErr == nil {
				fCopy := fetched
				state.routeIDSet[fetched.ID] = &fCopy
				routeForTrip = &fCopy
			} else {
				api.Logger.Warn("failed to fetch route for trip reference", "tripID", trip.ID, "routeID", trip.RouteID)
				continue
			}
		}
		references.Trips = append(references.Trips, *models.NewTripReference(
			utils.FormCombinedID(routeForTrip.AgencyID, trip.ID),
			utils.FormCombinedID(routeForTrip.AgencyID, trip.RouteID),
			utils.FormCombinedID(routeForTrip.AgencyID, trip.ServiceID),
			trip.TripHeadsign.String,
			"",
			strconv.FormatInt(trip.DirectionID.Int64, 10),
			utils.FormCombinedID(routeForTrip.AgencyID, trip.BlockID.String),
			utils.FormCombinedID(routeForTrip.AgencyID, trip.ShapeID.String),
		))
	}

	for _, route := range state.routeIDSet {
		references.Routes = append(references.Routes, models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String,
		))
		if !addedAgencyIDs[route.AgencyID] {
			if ag, agErr := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID); agErr == nil {
				references.Agencies = append(references.Agencies, models.NewAgencyReference(
					ag.ID, ag.Name, ag.Url, ag.Timezone, ag.Lang.String,
					ag.Phone.String, ag.Email.String, ag.FareUrl.String, "", false,
				))
				addedAgencyIDs[ag.ID] = true
			}
		}
	}

	stopIDsSlice := stringMapKeys(state.stopIDSet)
	batchStops, _ := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDsSlice)
	batchRoutesForStops, _ := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDsSlice)

	stopsMap := make(map[string]gtfsdb.Stop, len(batchStops))
	for _, s := range batchStops {
		stopsMap[s.ID] = s
	}
	routesByStop := make(map[string][]gtfsdb.GetRoutesForStopsRow)
	for _, row := range batchRoutesForStops {
		routesByStop[row.StopID] = append(routesByStop[row.StopID], row)
	}

	for _, sid := range stopIDsSlice {
		stopData, ok := stopsMap[sid]
		if !ok {
			continue
		}
		ag := state.stopAgencyMap[sid]
		if ag == "" {
			ag = state.stopAgencyOverride[sid]
		}
		if ag == "" {
			ag = state.fallbackAgencyID
		}
		routesForStop := routesByStop[sid]
		combinedRouteIDs := make([]string, len(routesForStop))
		for i, rr := range routesForStop {
			combinedRouteIDs[i] = utils.FormCombinedID(rr.AgencyID, rr.ID)
			if _, exists := state.routeIDSet[rr.ID]; !exists {
				rc := gtfsdb.Route{
					ID:        rr.ID,
					AgencyID:  rr.AgencyID,
					ShortName: rr.ShortName,
					LongName:  rr.LongName,
					Desc:      rr.Desc,
					Type:      rr.Type,
					Url:       rr.Url,
					Color:     rr.Color,
					TextColor: rr.TextColor,
				}
				state.routeIDSet[rr.ID] = &rc
			}
		}
		references.Stops = append(references.Stops, models.Stop{
			ID:                 utils.FormCombinedID(ag, stopData.ID),
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

	topLevelSituationIDs := make([]string, 0, len(state.collectedAlerts))
	if len(state.collectedAlerts) > 0 {
		alertSlice := make([]gtfs.Alert, 0, len(state.collectedAlerts))
		for alertID, a := range state.collectedAlerts {
			alertSlice = append(alertSlice, a)
			topLevelSituationIDs = append(topLevelSituationIDs, alertID)
		}
		references.Situations = append(references.Situations, api.BuildSituationReferences(alertSlice)...)
	}

	return references, topLevelSituationIDs
}

func (api *RestAPI) buildLocationQueriedStopIDs(stops []gtfsdb.Stop, state *locationArrivalsState) []string {
	queriedStopIDs := make([]string, 0, len(state.stopsWithArrivals))
	for _, dbStop := range stops {
		if state.stopsWithArrivals[dbStop.ID] {
			ag := state.stopAgencyMap[dbStop.ID]
			if ag == "" {
				ag = state.fallbackAgencyID
			}
			queriedStopIDs = append(queriedStopIDs, utils.FormCombinedID(ag, dbStop.ID))
		}
	}
	return queriedStopIDs
}

// getLocationNearbyStops returns stops near the query centre together with their
// distance from the centre, sorted ascending by distance.
func getLocationNearbyStops(
	api *RestAPI,
	ctx context.Context,
	centerLat, centerLon float64,
	queryTime time.Time,
) []models.StopWithDistance {

	nearby, _ := api.GtfsManager.GetStopsForLocation(
		ctx,
		&internalgtfs.LocationParams{
			Lat:    centerLat,
			Lon:    centerLon,
			Radius: models.DefaultSearchRadiusInMeters,
		},
		"",
		250,
		[]int{},
	)

	if len(nearby) == 0 {
		return nil
	}

	candidateIDs := make([]string, len(nearby))
	for i, s := range nearby {
		candidateIDs[i] = s.ID
	}

	nearbyAgencyMap := make(map[string]string, len(candidateIDs))
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, candidateIDs)
	if err != nil {
		api.Logger.Warn("failed to resolve agencies for nearby stops", "error", err)
	} else {
		for _, row := range agencyRows {
			if _, exists := nearbyAgencyMap[row.StopID]; !exists {
				nearbyAgencyMap[row.StopID] = row.ID
			}
		}
	}

	nearbyFallback := pickPrimaryAgency(nearbyAgencyMap)

	result := make([]models.StopWithDistance, 0, len(nearby))
	for _, s := range nearby {
		ag := nearbyFallback
		if resolved, ok := nearbyAgencyMap[s.ID]; ok {
			ag = resolved
		}
		combinedID := utils.FormCombinedID(ag, s.ID)

		dist := utils.Distance(centerLat, centerLon, s.Lat, s.Lon)
		result = append(result, models.StopWithDistance{
			StopID:            combinedID,
			DistanceFromQuery: dist,
		})
	}

	if len(result) == 0 {
		return nil
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].DistanceFromQuery < result[j].DistanceFromQuery
	})

	return result
}

// stringMapKeys returns the keys of a map[string]bool as a string slice.
func stringMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// pickPrimaryAgency returns the agency ID that appears most frequently in the
// stopCode→agencyID map. Used only as a fallback when a stop has no resolved
// agency — never used to prefix alert IDs directly.
func pickPrimaryAgency(stopAgencyMap map[string]string) string {
	counts := make(map[string]int, 4)
	for _, ag := range stopAgencyMap {
		counts[ag]++
	}
	best := ""
	bestCount := 0
	for ag, cnt := range counts {
		if cnt > bestCount || (cnt == bestCount && ag < best) {
			best = ag
			bestCount = cnt
		}
	}
	return best
}
