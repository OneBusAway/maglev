package restapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// Define params structure for the plural handler
type ArrivalsStopParams struct {
	MinutesAfter  int
	MinutesBefore int
	Time          time.Time
}

// parseArrivalsAndDeparturesParams parses and validates parameters.
func (api *RestAPI) parseArrivalsAndDeparturesParams(r *http.Request) (ArrivalsStopParams, map[string][]string) {
	const maxMinutesBefore = 60
	const maxMinutesAfter = 240

	params := ArrivalsStopParams{
		MinutesAfter:  35,              // Default
		MinutesBefore: 5,               // Default
		Time:          api.Clock.Now(), // Default to current time
	}

	var fieldErrors map[string][]string

	addError := func(field, msg string) {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		fieldErrors[field] = append(fieldErrors[field], msg)
	}

	query := r.URL.Query()

	if val := query.Get("minutesAfter"); val != "" {
		if minutes, err := strconv.Atoi(val); err == nil {
			if minutes > maxMinutesAfter {
				params.MinutesAfter = maxMinutesAfter
			} else if minutes >= 0 {
				params.MinutesAfter = minutes
			} else {
				addError("minutesAfter", "must be a non-negative integer")
			}
		} else {
			addError("minutesAfter", "must be a valid integer")
		}
	}

	if val := query.Get("minutesBefore"); val != "" {
		if minutes, err := strconv.Atoi(val); err == nil {
			if minutes > maxMinutesBefore {
				params.MinutesBefore = maxMinutesBefore
			} else if minutes >= 0 {
				params.MinutesBefore = minutes
			} else {
				addError("minutesBefore", "must be a non-negative integer")
			}
		} else {
			addError("minutesBefore", "must be a valid integer")
		}
	}

	if val := query.Get("time"); val != "" {
		if timeMs, err := strconv.ParseInt(val, 10, 64); err == nil {
			params.Time = time.Unix(timeMs/1000, (timeMs%1000)*1000000)
		} else {
			addError("time", "must be a valid Unix timestamp in milliseconds")
		}
	}

	return params, fieldErrors
}

func (api *RestAPI) arrivalsAndDeparturesForStopHandler(w http.ResponseWriter, r *http.Request) {
	parsed, _ := utils.GetParsedIDFromContext(r.Context())
	stopAgencyID := parsed.AgencyID
	stopCode := parsed.CodeID
	stopID := parsed.CombinedID

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Capture parsing errors
	params, fieldErrors := api.parseArrivalsAndDeparturesParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopCode)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, stopAgencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	loc := utils.LoadLocationWithUTCFallBack(agency.Timezone, stopAgencyID)
	params.Time = params.Time.In(loc)
	windowStart := params.Time.Add(-time.Duration(params.MinutesBefore) * time.Minute)
	windowEnd := params.Time.Add(time.Duration(params.MinutesAfter) * time.Minute)

	arrivals := make([]models.ArrivalAndDeparture, 0)
	references := models.NewEmptyReferences()

	// Add the stop's agency to references immediately
	references.Agencies = append(references.Agencies, models.NewAgencyReference(
		agency.ID, agency.Name, agency.Url, agency.Timezone, agency.Lang.String,
		agency.Phone.String, agency.Email.String, agency.FareUrl.String, "", false,
	))

	// Track which agencies we have already added to avoid duplicates
	addedAgencyIDs := make(map[string]bool)
	addedAgencyIDs[agency.ID] = true

	collectedAlerts := make(map[string]gtfs.Alert)
	alertAgencyID := stopAgencyID

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

		targetDate := params.Time.AddDate(0, 0, dayOffset)
		serviceMidnight := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, loc)
		serviceDateStr := targetDate.Format("20060102")

		activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDateStr)
		if err != nil {
			api.Logger.Warn("failed to query active service IDs",
				slog.String("date", serviceDateStr),
				slog.Any("error", err))
			continue
		}
		if len(activeServiceIDs) == 0 {
			continue
		}

		activeServiceIDSet := make(map[string]bool, len(activeServiceIDs))
		for _, sid := range activeServiceIDs {
			activeServiceIDSet[sid] = true
		}

		startNanos := windowStart.Sub(serviceMidnight).Nanoseconds()
		endNanos := windowEnd.Sub(serviceMidnight).Nanoseconds()

		if endNanos < 0 {
			continue
		}

		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForStopInWindow(ctx, gtfsdb.GetStopTimesForStopInWindowParams{
			StopID:           stopCode,
			WindowStartNanos: startNanos,
			WindowEndNanos:   endNanos,
		})
		if err != nil {
			api.Logger.Warn("failed to query stop times in window",
				slog.String("stopID", stopCode),
				slog.Any("error", err))
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
		response := models.NewArrivalsAndDepartureResponse(arrivals, references, []string{}, []string{}, stopID, api.Clock)
		api.sendResponse(w, r, response)
		return
	}

	// Maps for Caching and References
	tripIDSet := make(map[string]*gtfsdb.Trip)
	routeIDSet := make(map[string]*gtfsdb.Route)
	stopIDSet := make(map[string]bool)

	// Add the current stop
	stopIDSet[stop.ID] = true

	batchRouteIDs := make(map[string]bool)
	batchTripIDs := make(map[string]bool)

	for _, ast := range allActiveStopTimes {
		st := ast.GetStopTimesForStopInWindowRow
		if st.RouteID != "" {
			batchRouteIDs[st.RouteID] = true
		}
		if st.TripID != "" {
			batchTripIDs[st.TripID] = true
		}
	}

	uniqueRouteIDs := make([]string, 0, len(batchRouteIDs))
	for id := range batchRouteIDs {
		uniqueRouteIDs = append(uniqueRouteIDs, id)
	}

	uniqueTripIDs := make([]string, 0, len(batchTripIDs))
	for id := range batchTripIDs {
		uniqueTripIDs = append(uniqueTripIDs, id)
	}

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, uniqueRouteIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	allTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routesLookup := make(map[string]gtfsdb.Route)
	for _, route := range allRoutes {
		routesLookup[route.ID] = route
	}

	tripsLookup := make(map[string]gtfsdb.Trip)
	for _, trip := range allTrips {
		tripsLookup[trip.ID] = trip
	}

	// Batch-fetch stop counts per trip to avoid per-arrival N+1 queries for totalStopsInTrip.
	tripStopCountMap := make(map[string]int, len(uniqueTripIDs))
	if len(uniqueTripIDs) > 0 {
		allStopTimesForTrips, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, uniqueTripIDs)
		if err != nil {
			api.Logger.Warn("failed to batch fetch stop times for trips", slog.Any("error", err))
		} else {
			for _, st := range allStopTimesForTrips {
				tripStopCountMap[st.TripID]++
			}
		}
	}

	for _, ast := range allActiveStopTimes {
		st := ast.GetStopTimesForStopInWindowRow

		serviceMidnight := ast.ServiceDate
		serviceDateMillis := serviceMidnight.UnixMilli()
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

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

		rCopy := route
		routeIDSet[route.ID] = &rCopy
		tCopy := trip
		tripIDSet[trip.ID] = &tCopy

		scheduledArrivalTime := serviceMidnight.Add(time.Duration(st.ArrivalTime)).UnixMilli()
		scheduledDepartureTime := serviceMidnight.Add(time.Duration(st.DepartureTime)).UnixMilli()

		var (
			predictedArrivalTime   = scheduledArrivalTime
			predictedDepartureTime = scheduledDepartureTime
			predicted              = false
			vehicleID              string
			tripStatus             *models.TripStatusForTripDetails
			distanceFromStop       = 0.0
			numberOfStopsAway      = 0
		)

		// Get vehicle if available
		vehicle := api.GtfsManager.GetVehicleForTrip(ctx, st.TripID)
		if vehicle != nil && vehicle.Trip != nil {
			vehicleID = vehicle.ID.ID
		}

		// Prepare scheduled times for the shared function
		schedArrTime := serviceMidnight.Add(time.Duration(st.ArrivalTime))
		schedDepTime := serviceMidnight.Add(time.Duration(st.DepartureTime))

		// Call unified prediction logic
		predArr, predDep, isPredicted := api.getPredictedTimes(
			st.TripID,
			stopCode,
			int64(st.StopSequence),
			schedArrTime,
			schedDepTime,
		)

		if isPredicted {
			predicted = true
			predictedArrivalTime = predArr
			predictedDepartureTime = predDep
		}

		if vehicle != nil {
			// Use route.AgencyID instead of stopAgencyID for BuildTripStatus
			status, statusErr := api.BuildTripStatus(ctx, route.AgencyID, st.TripID, serviceMidnight, params.Time)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed for arrival",
					"tripID", st.TripID, "error", statusErr)
			}
			if status != nil {
				tripStatus = status

				if status.NextStop != "" {
					_, nextStopID, err := utils.ExtractAgencyIDAndCodeID(status.NextStop)
					if err == nil {
						stopIDSet[nextStopID] = true
					}
				}
				if status.ClosestStop != "" {
					_, closestStopID, err := utils.ExtractAgencyIDAndCodeID(status.ClosestStop)
					if err == nil {
						stopIDSet[closestStopID] = true
					}
				}

				if vehicle.Position != nil {
					distanceFromStop = api.getBlockDistanceToStop(ctx, st.TripID, stopCode, vehicle, params.Time)

					numberOfStopsAwayPtr := api.getNumberOfStopsAway(ctx, st.TripID, int(st.StopSequence), vehicle, params.Time)
					if numberOfStopsAwayPtr != nil {
						numberOfStopsAway = *numberOfStopsAwayPtr
					} else {
						numberOfStopsAway = -1
					}
				}

				// If there's an active trip that's different from the current trip, add it to references
				if status.ActiveTripID != "" {
					_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID)
					if err == nil && activeTripID != st.TripID {
						// Check cache for active trip
						if _, exists := tripIDSet[activeTripID]; !exists {
							activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID)
							if err != nil {
								api.Logger.Debug("skipping active trip reference: trip not found",
									slog.String("activeTripID", activeTripID),
									slog.String("scheduledTripID", st.TripID),
									slog.Any("error", err))
							} else {
								tripIDSet[activeTrip.ID] = &activeTrip
								activeRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, activeTrip.RouteID)
								if err == nil {
									routeIDSet[activeRoute.ID] = &activeRoute
								} else {
									api.Logger.Warn("failed to fetch route for active trip reference",
										"tripID", activeTripID, "routeID", activeTrip.RouteID, "error", err)
								}
							}
						}
					}
				}
			}
		}

		if !predicted {
			predictedArrivalTime = 0
			predictedDepartureTime = 0
		}

		totalStopsInTrip := tripStopCountMap[st.TripID]

		blockTripSequence := api.calculateBlockTripSequence(ctx, st.TripID, serviceMidnight)

		lastUpdateTime := api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
		var lastUpdateTimePtr *int64
		if lastUpdateTime > 0 {
			lastUpdateTimePtr = utils.Int64Ptr(lastUpdateTime)
		}

		tripAlerts := api.GtfsManager.GetAlertsForTrip(r.Context(), st.TripID)
		situationIDs := make([]string, 0, len(tripAlerts))
		for _, alert := range tripAlerts {
			if alert.ID == "" {
				continue
			}

			situationIDs = append(situationIDs, utils.FormCombinedID(route.AgencyID, alert.ID))
			if _, seen := collectedAlerts[alert.ID]; !seen {
				collectedAlerts[alert.ID] = alert
			}
		}

		if alertAgencyID == "" && route.AgencyID != "" {
			alertAgencyID = route.AgencyID
		}

		arrival := models.NewArrivalAndDeparture(
			utils.FormCombinedID(route.AgencyID, route.ID),  // routeID
			route.ShortName.String,                          // routeShortName
			route.LongName.String,                           // routeLongName
			utils.FormCombinedID(route.AgencyID, st.TripID), // tripID
			st.TripHeadsign.String,                          // tripHeadsign
			stopID,                                          // stopID
			vehicleID,                                       // vehicleID
			serviceDateMillis,                               // serviceDate
			scheduledArrivalTime,                            // scheduledArrivalTime
			scheduledDepartureTime,                          // scheduledDepartureTime
			predictedArrivalTime,                            // predictedArrivalTime
			predictedDepartureTime,                          // predictedDepartureTime
			lastUpdateTimePtr,                               // lastUpdateTime
			predicted,                                       // predicted
			true,                                            // arrivalEnabled
			true,                                            // departureEnabled
			int(st.StopSequence)-1,                          // stopSequence (Zero-based index)
			totalStopsInTrip,                                // totalStopsInTrip
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

		arrivals = append(arrivals, *arrival)
	}

	for _, trip := range tripIDSet {
		// Get the route to determine the correct agency for trip/route IDs
		var route *gtfsdb.Route
		var routeAgencyID string

		if r, ok := routeIDSet[trip.RouteID]; ok {
			route = r
			routeAgencyID = route.AgencyID
		} else {
			fetchedRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
			if err == nil {
				route = &fetchedRoute
				routeAgencyID = route.AgencyID
				routeIDSet[trip.RouteID] = route
			} else {
				api.Logger.Warn("failed to fetch route for trip reference", "tripID", trip.ID, "routeID", trip.RouteID, "error", err)
				continue // Skip instead of falling back to stopAgencyID
			}
		}

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

	// Batch-fetch all stop references in one shot instead of one query per stop.
	stopIDsSlice := make([]string, 0, len(stopIDSet))
	for sid := range stopIDSet {
		stopIDsSlice = append(stopIDsSlice, sid)
	}

	batchStops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDsSlice)
	if err != nil {
		api.Logger.Warn("failed to batch fetch stop references", slog.Any("error", err))
		batchStops = nil
	}

	batchRoutesForStops, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDsSlice)
	if err != nil {
		api.Logger.Warn("failed to batch fetch routes for stop references", slog.Any("error", err))
		batchRoutesForStops = nil
	}

	stopsMap := make(map[string]gtfsdb.Stop, len(batchStops))
	for _, s := range batchStops {
		stopsMap[s.ID] = s
	}

	routesByStop := make(map[string][]gtfsdb.GetRoutesForStopsRow)
	for _, row := range batchRoutesForStops {
		routesByStop[row.StopID] = append(routesByStop[row.StopID], row)
	}

	for stopID := range stopIDSet {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		stopData, ok := stopsMap[stopID]
		if !ok {
			api.Logger.Debug("skipping stop reference: stop not found", slog.String("stopID", stopID))
			continue
		}

		routesForThisStop := routesByStop[stopID]
		combinedRouteIDs := make([]string, len(routesForThisStop))
		for i, route := range routesForThisStop {
			// Use route.AgencyID instead of stopAgencyID
			combinedRouteIDs[i] = utils.FormCombinedID(route.AgencyID, route.ID)

			if _, exists := routeIDSet[route.ID]; !exists {
				routeCopy := gtfsdb.Route{
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
				routeIDSet[route.ID] = &routeCopy
			}
		}

		stopRef := models.Stop{
			ID:                 utils.FormCombinedID(stopAgencyID, stopData.ID),
			Name:               stopData.Name.String,
			Lat:                stopData.Lat,
			Lon:                stopData.Lon,
			Code:               stopData.Code.String,
			Direction:          api.DirectionCalculator.CalculateStopDirection(ctx, stopData.ID, stopData.Direction),
			LocationType:       int(stopData.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stopData.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		references.Stops = append(references.Stops, stopRef)
	}

	for _, route := range routeIDSet {
		routeRef := models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String)

		references.Routes = append(references.Routes, routeRef)

		// Add route agency to references if not already added
		if !addedAgencyIDs[route.AgencyID] {
			routeAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
			if err == nil {
				references.Agencies = append(references.Agencies, models.NewAgencyReference(
					routeAgency.ID, routeAgency.Name, routeAgency.Url, routeAgency.Timezone, routeAgency.Lang.String,
					routeAgency.Phone.String, routeAgency.Email.String, routeAgency.FareUrl.String, "", false,
				))
				addedAgencyIDs[route.AgencyID] = true
			} else {
				api.Logger.Warn("failed to fetch route agency for reference", "agencyID", route.AgencyID, "error", err)
			}
		}
	}

	for _, alert := range api.GtfsManager.GetAlertsForStop(stopCode) {
		if alert.ID != "" {
			if _, seen := collectedAlerts[alert.ID]; !seen {
				collectedAlerts[alert.ID] = alert
			}
		}
	}

	if len(collectedAlerts) > 0 {
		alertSlice := make([]gtfs.Alert, 0, len(collectedAlerts))
		for _, a := range collectedAlerts {
			alertSlice = append(alertSlice, a)
		}
		situations := api.BuildSituationReferences(alertSlice)
		references.Situations = append(references.Situations, situations...)
	}

	topLevelSituationIDSet := make(map[string]struct{}, len(collectedAlerts))
	for alertID := range collectedAlerts {
		topLevelSituationIDSet[utils.FormCombinedID(alertAgencyID, alertID)] = struct{}{}
	}
	topLevelSituationIDs := make([]string, 0, len(topLevelSituationIDSet))
	for id := range topLevelSituationIDSet {
		topLevelSituationIDs = append(topLevelSituationIDs, id)
	}

	nearbyStopIDs := getNearbyStopIDs(api, ctx, stop.Lat, stop.Lon, stopCode, stopAgencyID)
	response := models.NewArrivalsAndDepartureResponse(arrivals, references, nearbyStopIDs, topLevelSituationIDs, stopID, api.Clock)
	api.sendResponse(w, r, response)
}

func getNearbyStopIDs(api *RestAPI, ctx context.Context, lat, lon float64, stopID, fallbackAgencyID string) []string {
	nearbyStops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, 10000, 100, 100, "", 5, false, []int{}, api.Clock.Now())
	if len(nearbyStops) == 0 {
		return nil
	}

	// Collect nearby stop IDs (excluding the current stop) for a batch agency lookup.
	var candidateIDs []string
	for _, s := range nearbyStops {
		if s.ID != stopID {
			candidateIDs = append(candidateIDs, s.ID)
		}
	}
	if len(candidateIDs) == 0 {
		return nil
	}

	// Batch-resolve the owning agency for each nearby stop so that
	// multi-agency feeds produce correct combined IDs.
	stopAgencyMap := make(map[string]string, len(candidateIDs))
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, candidateIDs)
	if err != nil {
		api.Logger.Warn("failed to resolve agencies for nearby stops, using fallback",
			"error", err, "fallbackAgencyID", fallbackAgencyID)
	} else {
		for _, row := range agencyRows {
			// First agency wins; a stop served by multiple agencies uses the first one found.
			if _, exists := stopAgencyMap[row.StopID]; !exists {
				stopAgencyMap[row.StopID] = row.ID
			}
		}
	}

	nearbyStopIDs := make([]string, 0, len(candidateIDs))
	for _, sid := range candidateIDs {
		agency := fallbackAgencyID
		if resolved, ok := stopAgencyMap[sid]; ok {
			agency = resolved
		}
		nearbyStopIDs = append(nearbyStopIDs, utils.FormCombinedID(agency, sid))
	}
	return nearbyStopIDs
}
