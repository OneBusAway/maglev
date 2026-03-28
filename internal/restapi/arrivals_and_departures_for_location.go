package restapi

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
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

	Time          time.Time
	MinutesBefore int
	MinutesAfter  int

	MaxCount int
}

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
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		for k, v := range locErrors {
			fieldErrors[k] = append(fieldErrors[k], v...)
		}
	} else {
		params.Lat = loc.Lat
		params.Lon = loc.Lon
		params.Radius = loc.Radius
		params.LatSpan = loc.LatSpan
		params.LonSpan = loc.LonSpan
	}

	q := r.URL.Query()

	// time
	if val := q.Get("time"); val != "" {
		if ms, err := strconv.ParseInt(val, 10, 64); err == nil {
			params.Time = time.Unix(ms/1000, (ms%1000)*1_000_000)
		} else {
			addError("time", "must be a valid Unix timestamp in milliseconds")
		}
	}

	// minutesBefore
	if val := q.Get("minutesBefore"); val != "" {
		if n, err := strconv.Atoi(val); err != nil {
			addError("minutesBefore", "must be a valid integer")
		} else if n < 0 {
			addError("minutesBefore", "must be a non-negative integer")
		} else if n > maxMinutesBefore {
			params.MinutesBefore = maxMinutesBefore
		} else {
			params.MinutesBefore = n
		}
	}

	// minutesAfter
	if val := q.Get("minutesAfter"); val != "" {
		if n, err := strconv.Atoi(val); err != nil {
			addError("minutesAfter", "must be a valid integer")
		} else if n < 0 {
			addError("minutesAfter", "must be a non-negative integer")
		} else if n > maxMinutesAfter {
			params.MinutesAfter = maxMinutesAfter
		} else {
			params.MinutesAfter = n
		}
	}

	// maxCount — reuse the shared parser.
	var maxCountErrors map[string][]string
	params.MaxCount, maxCountErrors = utils.ParseMaxCount(q, defaultMaxCount, nil)
	if len(maxCountErrors) > 0 {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		for k, v := range maxCountErrors {
			fieldErrors[k] = append(fieldErrors[k], v...)
		}
	}

	return params, fieldErrors
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

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Find stops inside the bounding box using the in-memory R-tree spatial index.
	// Pass params.Time so that a historical `time=` override is respected.
	stops := api.GtfsManager.GetStopsForLocation(
		ctx,
		params.Lat, params.Lon,
		params.Radius,
		params.LatSpan, params.LonSpan,
		"",
		params.MaxCount,
		false,
		[]int{},
		params.Time,
	)

	if len(stops) == 0 {
		api.sendResponse(w, r, models.NewArrivalsAndDeparturesForLocationResponse(
			[]models.ArrivalAndDeparture{},
			*models.NewEmptyReferences(),
			[]models.StopWithDistance{},
			[]string{},
			[]string{},
			false,
			api.Clock,
		))
		return
	}

	// Collect raw stop codes (no agency prefix) for batch DB queries.
	rawStopCodes := make([]string, 0, len(stops))
	for _, s := range stops {
		rawStopCodes = append(rawStopCodes, s.ID)
	}

	// Resolve agency for each stop (needed to build combined "agencyId_stopCode" IDs).
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, rawStopCodes)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	// stopCode → agencyID; first agency wins for multi-agency stops.
	stopAgencyMap := make(map[string]string, len(agencyRows))
	for _, row := range agencyRows {
		if _, exists := stopAgencyMap[row.StopID]; !exists {
			stopAgencyMap[row.StopID] = row.ID
		}
	}

	// fallbackAgencyID is used only when a stop has no entry in stopAgencyMap
	// (e.g. a stop with no active routes). Derived from the most common agency
	// among the queried stops — never used to prefix alert IDs.
	fallbackAgencyID := pickPrimaryAgency(stopAgencyMap)

	// Determine the base query timezone from the fallback agency.
	agencyLoc := time.UTC
	if fallbackAgencyID != "" {
		if ag, tzErr := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, fallbackAgencyID); tzErr == nil {
			if parsed, parseErr := loadAgencyLocation(ag.ID, ag.Timezone); parseErr == nil {
				agencyLoc = parsed
			}
		}
	}

	// Fan out: collect arrivals across every stop in the bbox.
	arrivals := make([]models.ArrivalAndDeparture, 0, len(stops)*4)

	// Shared reference-collection maps (deduplicated across all stops).
	tripIDSet := make(map[string]*gtfsdb.Trip)
	routeIDSet := make(map[string]*gtfsdb.Route)
	stopIDSet := make(map[string]bool) // raw stop codes for reference building

	// Track which stop codes actually produced at least one arrival.
	// Java only includes a stop in the entry's stopIds when it has results.
	stopsWithArrivals := make(map[string]bool)

	collectedAlerts := make(map[string]gtfs.Alert)

	limitExceeded := false

	for _, dbStop := range stops {
		// Early exit once maxCount is reached — mirrors Java's MaxCountSupport.
		if limitExceeded {
			break
		}
		if len(arrivals) >= params.MaxCount {
			limitExceeded = true
			break
		}

		stopCode := dbStop.ID
		agencyID := stopAgencyMap[stopCode]
		if agencyID == "" {
			agencyID = fallbackAgencyID
		}
		combinedStopID := utils.FormCombinedID(agencyID, stopCode)
		stopIDSet[stopCode] = true

		// Per-stop timezone — handles multi-agency feeds where stops may span TZs.
		stopLoc := agencyLoc
		if agencyID != fallbackAgencyID {
			if ag, agErr := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID); agErr == nil {
				if parsed, parseErr := loadAgencyLocation(ag.ID, ag.Timezone); parseErr == nil {
					stopLoc = parsed
				}
			}
		}

		stopQueryTime := params.Time.In(stopLoc)
		stopWindowStart := stopQueryTime.Add(-time.Duration(params.MinutesBefore) * time.Minute)
		stopWindowEnd := stopQueryTime.Add(time.Duration(params.MinutesAfter) * time.Minute)

		// Query 3 days (yesterday/today/tomorrow) to handle overnight trips —
		// identical to the single-stop handler's approach.
		type activeStopTime struct {
			gtfsdb.GetStopTimesForStopInWindowRow
			ServiceDate time.Time
		}
		var allActiveStopTimes []activeStopTime

		for dayOffset := -1; dayOffset <= 1; dayOffset++ {
			if ctx.Err() != nil {
				api.clientCanceledResponse(w, r, ctx.Err())
				return
			}

			targetDate := stopQueryTime.AddDate(0, 0, dayOffset)
			serviceMidnight := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, stopLoc)
			serviceDateStr := targetDate.Format("20060102")

			activeServiceIDs, svcErr := api.GtfsManager.GetActiveServiceIDsForDateCached(ctx, serviceDateStr)
			if svcErr != nil {
				api.Logger.Warn("failed to query active service IDs",
					slog.String("date", serviceDateStr),
					slog.Any("error", svcErr))
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
				api.Logger.Warn("failed to query stop times in window",
					slog.String("stopID", stopCode),
					slog.Any("error", stErr))
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

		if len(allActiveStopTimes) == 0 {
			// This stop has no arrivals in the window — do not include it in stopIds.
			continue
		}

		// Batch-fetch routes & trips for this stop's active stop times.
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
			api.Logger.Warn("failed to batch fetch routes",
				slog.String("stopID", stopCode), slog.Any("error", rErr))
			continue
		}
		fetchedTrips, tErr := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
		if tErr != nil {
			api.Logger.Warn("failed to batch fetch trips",
				slog.String("stopID", stopCode), slog.Any("error", tErr))
			continue
		}

		routesLookup := make(map[string]gtfsdb.Route, len(fetchedRoutes))
		for _, rt := range fetchedRoutes {
			routesLookup[rt.ID] = rt
		}
		tripsLookup := make(map[string]gtfsdb.Trip, len(fetchedTrips))
		for _, tr := range fetchedTrips {
			tripsLookup[tr.ID] = tr
		}

		// Batch total-stop-count per trip (avoids N+1 for totalStopsInTrip field).
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

		// Build one ArrivalAndDeparture per active stop time.
		stopProducedArrival := false
		for _, ast := range allActiveStopTimes {
			// Respect maxCount mid-stop as well.
			if len(arrivals) >= params.MaxCount {
				limitExceeded = true
				break
			}

			if ctx.Err() != nil {
				api.clientCanceledResponse(w, r, ctx.Err())
				return
			}

			st := ast.GetStopTimesForStopInWindowRow
			serviceMidnight := ast.ServiceDate
			serviceDateMillis := serviceMidnight.UnixMilli()

			route, routeOK := routesLookup[st.RouteID]
			if !routeOK {
				api.Logger.Debug("skipping stop time: route not found",
					slog.String("routeID", st.RouteID), slog.String("tripID", st.TripID))
				continue
			}
			trip, tripOK := tripsLookup[st.TripID]
			if !tripOK {
				api.Logger.Debug("skipping stop time: trip not found",
					slog.String("tripID", st.TripID))
				continue
			}

			rCopy := route
			routeIDSet[route.ID] = &rCopy
			tCopy := trip
			tripIDSet[trip.ID] = &tCopy

			scheduledArrivalTime := serviceMidnight.Add(time.Duration(st.ArrivalTime)).UnixMilli()
			scheduledDepartureTime := serviceMidnight.Add(time.Duration(st.DepartureTime)).UnixMilli()

			var (
				predictedArrivalTime   int64
				predictedDepartureTime int64
				predicted              = false
				vehicleID              string
				tripStatus             *models.TripStatus
				distanceFromStop       = 0.0
				numberOfStopsAway      = 0
				lastUpdateTime         int64 // always emitted; 0 when no vehicle

				// FIX #4: derive status from schedule deviation instead of
				// always emitting "default". Falls back to "default" when
				// there is no real-time data.
				arrivalStatus = "default"
			)

			vehicle := api.GtfsManager.GetVehicleForTrip(ctx, st.TripID)
			if vehicle != nil && vehicle.Trip != nil && vehicle.ID != nil {
				vehicleID = vehicle.ID.ID
			}

			schedArrTime := serviceMidnight.Add(time.Duration(st.ArrivalTime))
			schedDepTime := serviceMidnight.Add(time.Duration(st.DepartureTime))

			predArr, predDep, isPredicted := api.getPredictedTimes(
				st.TripID, stopCode, int64(st.StopSequence),
				schedArrTime, schedDepTime,
			)
			if isPredicted {
				predicted = true
				predictedArrivalTime = predArr
				predictedDepartureTime = predDep
			}
			// When not predicted, leave predictedArrivalTime/predictedDepartureTime as 0
			// (matches Java which emits 0 for unpredicted arrivals).

			// Gate BuildTripStatus on vehicle presence — matches the stop handler convention.
			if vehicle != nil {
				status, statusErr := api.BuildTripStatus(ctx, route.AgencyID, st.TripID, vehicle, serviceMidnight, stopQueryTime)
				if statusErr != nil {
					api.Logger.Warn("BuildTripStatus failed",
						"tripID", st.TripID, "error", statusErr)
				}
				if status != nil {
					tripStatus = status

					// Only set a meaningful status when the arrival is predicted.
					// Unpredicted arrivals stay "default".
					if predicted {
						arrivalStatus = arrivalStatusFromDeviation(status.ScheduleDeviation)
					}

					// Collect stops referenced in trip status for the references block.
					if status.NextStop != "" {
						if _, nsID, nsErr := utils.ExtractAgencyIDAndCodeID(status.NextStop); nsErr == nil {
							stopIDSet[nsID] = true
						}
					}
					if status.ClosestStop != "" {
						if _, csID, csErr := utils.ExtractAgencyIDAndCodeID(status.ClosestStop); csErr == nil {
							stopIDSet[csID] = true
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

					// Ensure the active trip (if different from scheduled) is in references.
					if status.ActiveTripID != "" {
						if _, atID, atErr := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID); atErr == nil && atID != st.TripID {
							if _, exists := tripIDSet[atID]; !exists {
								if at, atFetchErr := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, atID); atFetchErr == nil {
									atCopy := at
									tripIDSet[at.ID] = &atCopy
									if ar, arFetchErr := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, at.RouteID); arFetchErr == nil {
										arCopy := ar
										routeIDSet[ar.ID] = &arCopy
									}
								}
							}
						}
					}
				}

				rawUpdate := api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
				if rawUpdate > 0 {
					lastUpdateTime = rawUpdate
				}
			}

			totalStopsInTrip := tripStopCountMap[st.TripID]
			blockTripSequence := api.calculateBlockTripSequence(ctx, st.TripID, serviceMidnight)

			// alert.ID from GTFS-RT already contains the agency prefix (e.g. "40_16931").
			// Do NOT wrap with FormCombinedID — that would double-prefix to "40_40_16931".
			// Both per-arrival situationIds and top-level situationIds use the raw alert.ID.
			tripAlerts := api.GtfsManager.GetAlertsForTrip(ctx, st.TripID)
			situationIDs := make([]string, 0, len(tripAlerts))
			for _, alert := range tripAlerts {
				if alert.ID == "" {
					continue
				}
				situationIDs = append(situationIDs, alert.ID)
				if _, seen := collectedAlerts[alert.ID]; !seen {
					collectedAlerts[alert.ID] = alert
				}
			}

			// lastUpdateTimePtr: use a pointer so the model's omitempty drops it only
			// when the value is truly absent. We pass nil when lastUpdateTime==0 to
			// preserve the existing model behaviour (field omitted rather than 0).
			var lastUpdateTimePtr *int64
			if lastUpdateTime > 0 {
				lastUpdateTimePtr = utils.Int64Ptr(lastUpdateTime)
			}

			// vehicleID must carry the agency prefix to match Java output ("1_6853").
			formattedVehicleID := ""
			if vehicleID != "" {
				formattedVehicleID = utils.FormCombinedID(route.AgencyID, vehicleID)
			}

			// FIX #2: use raw GTFS stop_sequence (1-based) — do NOT subtract 1.
			// Java's ArrivalAndDepartureBean.getStopSequence() returns the raw
			// GTFS value directly; there is no zero-indexing in the wire format.
			rawStopSequence := int(st.StopSequence)

			arrivals = append(arrivals, *models.NewArrivalAndDeparture(
				utils.FormCombinedID(route.AgencyID, route.ID),  // routeID
				route.ShortName.String,                          // routeShortName
				route.LongName.String,                           // routeLongName
				utils.FormCombinedID(route.AgencyID, st.TripID), // tripID
				st.TripHeadsign.String,                          // tripHeadsign
				combinedStopID,                                  // stopID
				formattedVehicleID,                              // vehicleID (agency-prefixed or empty)
				serviceDateMillis,                               // serviceDate
				scheduledArrivalTime,                            // scheduledArrivalTime
				scheduledDepartureTime,                          // scheduledDepartureTime
				predictedArrivalTime,                            // predictedArrivalTime  (0 when unpredicted)
				predictedDepartureTime,                          // predictedDepartureTime (0 when unpredicted)
				lastUpdateTimePtr,                               // lastUpdateTime
				predicted,                                       // predicted
				true,                                            // arrivalEnabled
				true,                                            // departureEnabled
				rawStopSequence,                                 // FIX #2: raw GTFS stop_sequence, not zero-based
				totalStopsInTrip,                                // totalStopsInTrip
				numberOfStopsAway,                               // numberOfStopsAway
				blockTripSequence,                               // blockTripSequence
				distanceFromStop,                                // distanceFromStop
				arrivalStatus,                                   // FIX #4: derived from scheduleDeviation
				"",                                              // occupancyStatus
				"",                                              // predictedOccupancy
				"",                                              // historicalOccupancy
				tripStatus,                                      // tripStatus
				situationIDs,                                    // situationIDs (agency-prefixed)
			))
			stopProducedArrival = true
		}

		if stopProducedArrival {
			stopsWithArrivals[stopCode] = true
		}
	}

	// Sort arrivals by predicted (or scheduled) arrival time ascending.
	// Matches Java's ArrivalAndDepartureComparator.
	sort.Slice(arrivals, func(i, j int) bool {
		ti := arrivals[i].PredictedArrivalTime
		if ti == 0 {
			ti = arrivals[i].ScheduledArrivalTime
		}
		tj := arrivals[j].PredictedArrivalTime
		if tj == 0 {
			tj = arrivals[j].ScheduledArrivalTime
		}
		return ti < tj
	})

	// Build references block (agencies, routes, stops, trips, situations).
	references := models.NewEmptyReferences()
	addedAgencyIDs := make(map[string]bool)

	// Trips
	for _, trip := range tripIDSet {
		routeForTrip, ok := routeIDSet[trip.RouteID]
		if !ok {
			fetched, fErr := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
			if fErr != nil {
				api.Logger.Warn("failed to fetch route for trip reference",
					"tripID", trip.ID, "routeID", trip.RouteID, "error", fErr)
				continue
			}
			fCopy := fetched
			routeIDSet[fetched.ID] = &fCopy
			routeForTrip = &fCopy
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

	// Routes + their agencies.
	for _, route := range routeIDSet {
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
			ag, agErr := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
			if agErr == nil {
				references.Agencies = append(references.Agencies, models.NewAgencyReference(
					ag.ID, ag.Name, ag.Url, ag.Timezone, ag.Lang.String,
					ag.Phone.String, ag.Email.String, ag.FareUrl.String, "", false,
				))
				addedAgencyIDs[ag.ID] = true
			} else {
				api.Logger.Warn("failed to fetch agency for reference",
					"agencyID", route.AgencyID, "error", agErr)
			}
		}
	}

	// Stops (queried stops + nextStop/closestStop referenced by TripStatus).
	stopIDsSlice := make([]string, 0, len(stopIDSet))
	for sid := range stopIDSet {
		stopIDsSlice = append(stopIDsSlice, sid)
	}

	batchStops, bsErr := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDsSlice)
	if bsErr != nil {
		api.Logger.Warn("failed to batch fetch stop references", slog.Any("error", bsErr))
	}
	batchRoutesForStops, brsErr := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDsSlice)
	if brsErr != nil {
		api.Logger.Warn("failed to batch fetch routes for stops", slog.Any("error", brsErr))
	}

	stopsMap := make(map[string]gtfsdb.Stop, len(batchStops))
	for _, s := range batchStops {
		stopsMap[s.ID] = s
	}
	routesByStop := make(map[string][]gtfsdb.GetRoutesForStopsRow)
	for _, row := range batchRoutesForStops {
		routesByStop[row.StopID] = append(routesByStop[row.StopID], row)
	}

	for _, sid := range stopIDsSlice {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}
		stopData, ok := stopsMap[sid]
		if !ok {
			continue
		}
		ag := stopAgencyMap[sid]
		if ag == "" {
			ag = fallbackAgencyID
		}
		routesForStop := routesByStop[sid]
		combinedRouteIDs := make([]string, len(routesForStop))
		for i, rr := range routesForStop {
			combinedRouteIDs[i] = utils.FormCombinedID(rr.AgencyID, rr.ID)
			if _, exists := routeIDSet[rr.ID]; !exists {
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
				routeIDSet[rr.ID] = &rc
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
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stopData.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		})
	}

	// Collect stop-level service alerts.
	// These fall back to fallbackAgencyID for the agency prefix since there is
	// no route context available at the stop level.
	for _, sc := range rawStopCodes {
		for _, alert := range api.GtfsManager.GetAlertsForStop(sc) {
			if alert.ID != "" {
				if _, seen := collectedAlerts[alert.ID]; !seen {
					collectedAlerts[alert.ID] = alert
				}
			}
		}
	}

	// Build situation references and top-level situationIds.
	// Entry-level situationIds use the raw alert ID (e.g. "1_85725", "40_16559").
	// Alert IDs from GTFS-RT already contain the agency prefix, so no extra
	// FormCombinedID wrapping is applied here.
	// Per-arrival situationIds DO wrap with FormCombinedID — that is separate.
	topLevelSituationIDs := make([]string, 0, len(collectedAlerts))
	if len(collectedAlerts) > 0 {
		alertSlice := make([]gtfs.Alert, 0, len(collectedAlerts))
		for _, a := range collectedAlerts {
			alertSlice = append(alertSlice, a)
		}
		references.Situations = append(references.Situations, api.BuildSituationReferences(alertSlice)...)
		for alertID := range collectedAlerts {
			topLevelSituationIDs = append(topLevelSituationIDs, alertID)
		}
	}

	// Build the entry's stopIds — only stops that produced at least one arrival,
	// in the same order as the original stops slice (deterministic, not map order).
	// Java: stops are only added when !arrivalsAndDepartures.isEmpty().
	queriedStopIDs := make([]string, 0, len(stopsWithArrivals))
	for _, dbStop := range stops {
		if stopsWithArrivals[dbStop.ID] {
			ag := stopAgencyMap[dbStop.ID]
			if ag == "" {
				ag = fallbackAgencyID
			}
			queriedStopIDs = append(queriedStopIDs, utils.FormCombinedID(ag, dbStop.ID))
		}
	}

	// Build nearbyStopIds as []StopWithDistance.
	//
	// FIX #3: pass queriedStopIDs so that stops already in the entry's stopIds
	// are excluded from nearbyStopIds. Java's includeInputIdsInNearby=false
	// default means the bbox stops must not appear in both lists.
	nearbyStops := getLocationNearbyStops(api, ctx, params.Lat, params.Lon, params.Time, queriedStopIDs)

	api.sendResponse(w, r, models.NewArrivalsAndDeparturesForLocationResponse(
		arrivals,
		*references,
		nearbyStops,
		topLevelSituationIDs,
		queriedStopIDs,
		limitExceeded,
		api.Clock,
	))
}

// getLocationNearbyStops returns stops near the query centre together with their
// distance from the centre, sorted ascending by distance.
//
// Java equivalent: getNearbyStops() in StopWithArrivalsAndDeparturesBeanServiceImpl,
// which calls SphericalGeometryLibrary.distance() to populate distanceFromQuery.
//
// FIX #3: queriedStopIDs are excluded from the result so that stops already
// present in entry.stopIds do not also appear in entry.nearbyStopIds.
// This matches Java's includeInputIdsInNearby=false default behaviour.
func getLocationNearbyStops(
	api *RestAPI,
	ctx context.Context,
	centerLat, centerLon float64,
	queryTime time.Time,
	queriedStopIDs []string, // stops already in entry.stopIds — must be excluded
) []models.StopWithDistance {

	nearby := api.GtfsManager.GetStopsForLocation(
		ctx, centerLat, centerLon, 100, 0, 0, "", 250, false, []int{}, queryTime,
	)

	if len(nearby) == 0 {
		return nil
	}

	// Batch-resolve owning agency for each nearby stop.
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

	// pickPrimaryAgency over the nearby set for stops with no resolved agency.
	nearbyFallback := pickPrimaryAgency(nearbyAgencyMap)

	// Build a set of already-queried combined stop IDs for O(1) lookup.
	// FIX #3: Java excludes these via includeInputIdsInNearby=false.
	queriedSet := make(map[string]bool, len(queriedStopIDs))
	for _, id := range queriedStopIDs {
		queriedSet[id] = true
	}

	result := make([]models.StopWithDistance, 0, len(nearby))
	for _, s := range nearby {
		ag := nearbyFallback
		if resolved, ok := nearbyAgencyMap[s.ID]; ok {
			ag = resolved
		}
		combinedID := utils.FormCombinedID(ag, s.ID)

		// FIX #3: skip stops that are already in entry.stopIds.
		if queriedSet[combinedID] {
			continue
		}

		dist := utils.Distance(centerLat, centerLon, s.Lat, s.Lon)
		result = append(result, models.StopWithDistance{
			StopID:            combinedID,
			DistanceFromQuery: dist,
		})
	}

	if len(result) == 0 {
		return nil
	}

	// Sort by distance ascending to match Java's ordering.
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
