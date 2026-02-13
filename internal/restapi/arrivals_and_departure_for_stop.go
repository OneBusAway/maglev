package restapi

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	GTFS "maglev.onebusaway.org/internal/gtfs"
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
	stopID := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(stopID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, stopCode, err := utils.ExtractAgencyIDAndCodeID(stopID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

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

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	loc := utils.LoadLocationWithUTCFallBack(agency.Timezone, agencyID)
	params.Time = params.Time.In(loc)
	windowStart := params.Time.Add(-time.Duration(params.MinutesBefore) * time.Minute)
	windowEnd := params.Time.Add(time.Duration(params.MinutesAfter) * time.Minute)

	windowStartNanos := convertToNanosSinceMidnight(windowStart)
	windowEndNanos := convertToNanosSinceMidnight(windowEnd)

	serviceDate := params.Time.Format("20060102")
	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	arrivals := make([]models.ArrivalAndDeparture, 0)
	references := models.NewEmptyReferences()

	references.Agencies = append(references.Agencies, models.NewAgencyReference(
		agency.ID, agency.Name, agency.Url, agency.Timezone, agency.Lang.String,
		agency.Phone.String, agency.Email.String, agency.FareUrl.String, "", false,
	))

	if len(activeServiceIDs) == 0 {
		response := models.NewArrivalsAndDepartureResponse(arrivals, references, []string{}, []string{}, stopID, api.Clock)
		api.sendResponse(w, r, response)
		return
	}

	// Get trips that serve this stop and are active today
	activeTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByServiceID(ctx, activeServiceIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	activeTripIDs := make(map[string]bool)
	for _, trip := range activeTrips {
		activeTripIDs[trip.ID] = true
	}

	// Get all stop times for this stop within the time window
	allStopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForStopInWindow(ctx, gtfsdb.GetStopTimesForStopInWindowParams{
		StopID:           stopCode,
		WindowStartNanos: windowStartNanos,
		WindowEndNanos:   windowEndNanos,
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Filter stop times to only include active trips
	var stopTimes []gtfsdb.GetStopTimesForStopInWindowRow
	for _, st := range allStopTimes {
		if activeTripIDs[st.TripID] {
			stopTimes = append(stopTimes, st)
		}
	}

	// Maps for Caching and References
	tripIDSet := make(map[string]*gtfsdb.Trip)
	routeIDSet := make(map[string]*gtfsdb.Route)
	stopIDSet := make(map[string]bool)

	// Add the current stop
	stopIDSet[stop.ID] = true

	serviceDateMillis := params.Time.UnixMilli()
	serviceMidnight := time.Date(
		params.Time.Year(),
		params.Time.Month(),
		params.Time.Day(),
		0, 0, 0, 0,
		loc,
	)

	batchRouteIDs := make(map[string]bool)
	batchTripIDs := make(map[string]bool)

	for _, st := range stopTimes {
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

	for _, st := range stopTimes {
		if ctx.Err() != nil {
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

		// Get real-time updates from GTFS-RT
		vehicle := api.GtfsManager.GetVehicleForTrip(st.TripID)
		if vehicle != nil && vehicle.Trip != nil {
			vehicleID = vehicle.ID.ID

			// Fetch the Trip Update separately
			tripUpdate, _ := api.GtfsManager.GetTripUpdateByID(st.TripID)

			// Use the tripUpdate for predictions
			if tripUpdate != nil && len(tripUpdate.StopTimeUpdates) > 0 {
				// Look for StopTimeUpdate that matches this stop
				for _, stopTimeUpdate := range tripUpdate.StopTimeUpdates {
					// Match by stop sequence or stop ID
					if (stopTimeUpdate.StopSequence != nil && int64(*stopTimeUpdate.StopSequence) == st.StopSequence) ||
						(stopTimeUpdate.StopID != nil && *stopTimeUpdate.StopID == stopCode) {

						predicted = true

						// Update predicted times from GTFS-RT
						if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Time != nil {
							predictedArrivalTime = stopTimeUpdate.Arrival.Time.Unix() * 1000
						} else if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Delay != nil {
							predictedArrivalTime = scheduledArrivalTime + (stopTimeUpdate.Arrival.Delay.Nanoseconds() / 1e6)
						}

						if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Time != nil {
							predictedDepartureTime = stopTimeUpdate.Departure.Time.Unix() * 1000
						} else if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Delay != nil {
							predictedDepartureTime = scheduledDepartureTime + (stopTimeUpdate.Departure.Delay.Nanoseconds() / 1e6)
						}
						break
					}
				}
			}

			if !predicted && vehicle.Position != nil {
				predicted = true
				predictedArrivalTime = scheduledArrivalTime
				predictedDepartureTime = scheduledDepartureTime
			}
		}

		if vehicle != nil {
			status, _ := api.BuildTripStatus(ctx, agencyID, st.TripID, params.Time, params.Time)
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

		tripStopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, st.TripID)
		var totalStopsInTrip int
		if err != nil {
			api.Logger.Debug("failed to get stop times for trip",
				slog.String("tripID", st.TripID),
				slog.Any("error", err))
			totalStopsInTrip = 0
		} else {
			totalStopsInTrip = len(tripStopTimes)
		}

		blockTripSequence := api.calculateBlockTripSequence(ctx, st.TripID, params.Time)

		arrival := models.NewArrivalAndDeparture(
			utils.FormCombinedID(agencyID, route.ID),  // routeID
			route.ShortName.String,                    // routeShortName
			route.LongName.String,                     // routeLongName
			utils.FormCombinedID(agencyID, st.TripID), // tripID
			st.TripHeadsign.String,                    // tripHeadsign
			stopID,                                    // stopID
			vehicleID,                                 // vehicleID
			serviceDateMillis,                         // serviceDate
			scheduledArrivalTime,                      // scheduledArrivalTime
			scheduledDepartureTime,                    // scheduledDepartureTime
			predictedArrivalTime,                      // predictedArrivalTime
			predictedDepartureTime,                    // predictedDepartureTime
			params.Time.UnixMilli(),                   // lastUpdateTime
			predicted,                                 // predicted
			true,                                      // arrivalEnabled
			true,                                      // departureEnabled
			int(st.StopSequence)-1,                    // stopSequence (Zero-based)
			totalStopsInTrip,                          // totalStopsInTrip
			numberOfStopsAway,                         // numberOfStopsAway
			blockTripSequence,                         // blockTripSequence
			distanceFromStop,                          // distanceFromStop
			"default",                                 // status
			"",                                        // occupancyStatus
			"",                                        // predictedOccupancy
			"",                                        // historicalOccupancy
			tripStatus,                                // tripStatus
			api.GetSituationIDsForTrip(r.Context(), st.TripID), // situationIDs
		)

		arrivals = append(arrivals, *arrival)
	}

	for _, trip := range tripIDSet {
		tripRef := models.NewTripReference(
			utils.FormCombinedID(agencyID, trip.ID),
			utils.FormCombinedID(agencyID, trip.RouteID),
			utils.FormCombinedID(agencyID, trip.ServiceID),
			trip.TripHeadsign.String,
			"",
			trip.DirectionID.Int64,
			utils.FormCombinedID(agencyID, trip.BlockID.String),
			utils.FormCombinedID(agencyID, trip.ShapeID.String),
		)
		references.Trips = append(references.Trips, tripRef)
	}

	calc := GTFS.NewAdvancedDirectionCalculator(api.GtfsManager.GtfsDB.Queries)

	for stopID := range stopIDSet {
		if ctx.Err() != nil {
			return
		}

		stopData, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
		if err != nil {
			api.Logger.Debug("skipping stop reference: stop not found",
				slog.String("stopID", stopID),
				slog.Any("error", err))
			continue
		}

		routesForThisStop, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, []string{stopID})
		if err != nil {
			api.Logger.Debug("failed to get routes for stop",
				slog.String("stopID", stopID),
				slog.Any("error", err))
		}
		combinedRouteIDs := make([]string, len(routesForThisStop))
		for i, route := range routesForThisStop {
			combinedRouteIDs[i] = utils.FormCombinedID(agencyID, route.ID)
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
			ID:                 utils.FormCombinedID(agencyID, stopData.ID),
			Name:               stopData.Name.String,
			Lat:                stopData.Lat,
			Lon:                stopData.Lon,
			Code:               stopData.Code.String,
			Direction:          calc.CalculateStopDirection(ctx, stopData.ID, stopData.Direction),
			LocationType:       int(stopData.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stopData.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		references.Stops = append(references.Stops, stopRef)
	}

	for _, route := range routeIDSet {
		routeRef := models.NewRoute(
			utils.FormCombinedID(agencyID, route.ID),
			agencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String,
			route.ShortName.String,
		)
		references.Routes = append(references.Routes, routeRef)
	}

	nearbyStopIDs := getNearbyStopIDs(api, ctx, stop.Lat, stop.Lon, stopCode, agencyID)
	response := models.NewArrivalsAndDepartureResponse(arrivals, references, nearbyStopIDs, []string{}, stopID, api.Clock)
	api.sendResponse(w, r, response)
}

func convertToNanosSinceMidnight(t time.Time) int64 {
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	duration := t.Sub(midnight)
	return duration.Nanoseconds()
}

func getNearbyStopIDs(api *RestAPI, ctx context.Context, lat, lon float64, stopID, agencyID string) []string {
	nearbyStops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, 10000, 100, 100, "", 5, false, []int{}, api.Clock.Now())
	var nearbyStopIDs []string
	for _, s := range nearbyStops {
		if s.ID != stopID {
			nearbyStopIDs = append(nearbyStopIDs, utils.FormCombinedID(agencyID, s.ID))
		}
	}
	return nearbyStopIDs
}
