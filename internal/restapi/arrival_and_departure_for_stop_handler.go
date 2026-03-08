package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type ArrivalAndDepartureParams struct {
	MinutesAfter  int
	MinutesBefore int
	Time          *time.Time
	TripID        string
	ServiceDate   *time.Time
	VehicleID     string
	StopSequence  *int
}

// parseArrivalAndDepartureParams parses and validates request parameters.
// Returns parameters and a map of validation errors if any.
func (api *RestAPI) parseArrivalAndDepartureParams(r *http.Request, loc ...*time.Location) (ArrivalAndDepartureParams, map[string][]string) {
	params := ArrivalAndDepartureParams{
		MinutesAfter:  30, // Default 30 minutes after
		MinutesBefore: 5,  // Default 5 minutes before
	}

	// Initialize errors map
	fieldErrors := make(map[string][]string)

	// Validate minutesAfter
	if minutesAfterStr := r.URL.Query().Get("minutesAfter"); minutesAfterStr != "" {
		if minutesAfter, err := strconv.Atoi(minutesAfterStr); err == nil {
			params.MinutesAfter = minutesAfter
		} else {
			fieldErrors["minutesAfter"] = []string{"must be a valid integer"}
		}
	}

	// Validate minutesBefore
	if minutesBeforeStr := r.URL.Query().Get("minutesBefore"); minutesBeforeStr != "" {
		if minutesBefore, err := strconv.Atoi(minutesBeforeStr); err == nil {
			params.MinutesBefore = minutesBefore
		} else {
			fieldErrors["minutesBefore"] = []string{"must be a valid integer"}
		}
	}

	// Validate time
	if timeStr := r.URL.Query().Get("time"); timeStr != "" {
		if timeMs, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
			timeParam := time.Unix(timeMs/1000, 0)
			params.Time = &timeParam
		} else {
			fieldErrors["time"] = []string{"must be a valid Unix timestamp in milliseconds"}
		}
	}

	// Check TripID (Assignment only, required check is in handler)
	if tripIDStr := r.URL.Query().Get("tripId"); tripIDStr != "" {
		params.TripID = tripIDStr
	}

	// Validate serviceDate
	if serviceDateStr := r.URL.Query().Get("serviceDate"); serviceDateStr != "" {
		if serviceDateMs, err := strconv.ParseInt(serviceDateStr, 10, 64); err == nil {
			serviceDate := time.Unix(serviceDateMs/1000, 0)
			params.ServiceDate = &serviceDate
		} else {
			fieldErrors["serviceDate"] = []string{"must be a valid Unix timestamp in milliseconds"}
		}
	}

	// Optional vehicleId parameter
	if vehicleIDStr := r.URL.Query().Get("vehicleId"); vehicleIDStr != "" {
		params.VehicleID = vehicleIDStr
	}

	// Validate stopSequence
	if stopSequenceStr := r.URL.Query().Get("stopSequence"); stopSequenceStr != "" {
		if stopSequence, err := strconv.Atoi(stopSequenceStr); err == nil {
			params.StopSequence = &stopSequence
		} else {
			fieldErrors["stopSequence"] = []string{"must be a valid integer"}
		}
	}

	// Return errors if any existed
	if len(fieldErrors) > 0 {
		return params, fieldErrors
	}

	// If a timezone location was provided, localize serviceDate and time so that
	// callers receive times in the agency's timezone by default. This prevents the
	// bug where time.Unix(ms/1000, 0) creates a UTC time and downstream
	// Year()/Month()/Day()/Format() calls extract the wrong calendar date for
	// agencies in positive UTC offsets.
	if len(loc) > 0 && loc[0] != nil {
		if params.ServiceDate != nil {
			localized := params.ServiceDate.In(loc[0])
			params.ServiceDate = &localized
		}
		if params.Time != nil {
			localized := params.Time.In(loc[0])
			params.Time = &localized
		}
	}

	return params, nil
}

func (api *RestAPI) arrivalAndDepartureForStopHandler(w http.ResponseWriter, r *http.Request) {
	parsed, _ := utils.GetParsedIDFromContext(r.Context())
	stopAgencyID := parsed.AgencyID
	stopCode := parsed.CodeID
	stopID := parsed.CombinedID

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Capture parsing errors (syntax validation only — localization happens below
	// once we know the agency timezone).
	params, fieldErrors := api.parseArrivalAndDepartureParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	if params.TripID == "" {
		fieldErrors := map[string][]string{
			"tripId": {"missingRequiredField"},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	if params.ServiceDate == nil {
		fieldErrors := map[string][]string{
			"serviceDate": {"missingRequiredField"},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	_, tripID, err := utils.ExtractAgencyIDAndCodeID(params.TripID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopCode)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	stopAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, stopAgencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Localize serviceDate and time to the agency's timezone now that we know it.
	// This ensures Year()/Month()/Day()/Format() extract the correct local date.
	loc := utils.LoadLocationWithUTCFallBack(stopAgency.Timezone, stopAgency.ID)
	if params.ServiceDate != nil {
		localized := params.ServiceDate.In(loc)
		params.ServiceDate = &localized
	}
	if params.Time != nil {
		localized := params.Time.In(loc)
		params.Time = &localized
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	var targetStopTime *struct {
		ArrivalTime   int64
		DepartureTime int64
		StopSequence  int64
		StopHeadsign  string
	}

	for _, st := range stopTimes {
		if st.StopID == stopCode {
			if params.StopSequence != nil && int64(*params.StopSequence) != st.StopSequence {
				continue
			}
			targetStopTime = &struct {
				ArrivalTime   int64
				DepartureTime int64
				StopSequence  int64
				StopHeadsign  string
			}{
				ArrivalTime:   st.ArrivalTime,
				DepartureTime: st.DepartureTime,
				StopSequence:  st.StopSequence,
				StopHeadsign:  st.StopHeadsign.String,
			}
			break
		}
	}

	if targetStopTime == nil {
		api.sendNotFound(w, r)
		return
	}

	// Set current time
	var currentTime time.Time
	if params.Time != nil {
		currentTime = *params.Time
	} else {
		currentTime = api.Clock.Now().In(loc)
	}

	// serviceDate is already localized above; extract midnight in agency's TZ.
	serviceDate := *params.ServiceDate
	serviceMidnight := time.Date(
		serviceDate.Year(),
		serviceDate.Month(),
		serviceDate.Day(),
		0, 0, 0, 0,
		loc,
	)
	serviceDateMillis := serviceMidnight.UnixMilli()

	// Arrival time is stored in nanoseconds since midnight → convert to duration
	// arrival and departure time is stored in nanoseconds (sqlite)
	arrivalOffset := time.Duration(targetStopTime.ArrivalTime)
	departureOffset := time.Duration(targetStopTime.DepartureTime)

	// Add offsets to midnight
	scheduledArrivalTime := serviceMidnight.Add(arrivalOffset)
	scheduledDepartureTime := serviceMidnight.Add(departureOffset)

	// Convert to ms since epoch
	scheduledArrivalTimeMs := scheduledArrivalTime.UnixMilli()
	scheduledDepartureTimeMs := scheduledDepartureTime.UnixMilli()

	// Get real-time data for this trip if available
	var (
		predictedArrivalTime, predictedDepartureTime int64
		predicted                                    bool
		vehicleID                                    string
		tripStatus                                   *models.TripStatusForTripDetails
		distanceFromStop                             float64
		numberOfStopsAway                            int
	)

	// If vehicleId is provided, validate it matches the trip
	var vehicle *gtfs.Vehicle
	if params.VehicleID != "" {
		_, providedVehicleID, err := utils.ExtractAgencyIDAndCodeID(params.VehicleID)
		if err == nil {
			v, err := api.GtfsManager.GetVehicleByID(providedVehicleID)
			// If vehicle is found, validate it matches the trip
			if err == nil && v != nil && v.Trip != nil && v.Trip.ID.ID == tripID {
				vehicle = v
			}
		} else {
			api.Logger.Warn("malformed vehicleId provided",
				"vehicleId", params.VehicleID,
				"error", err)
		}
	} else {
		// If vehicleId is not provided, get the vehicle for the trip
		vehicle = api.GtfsManager.GetVehicleForTrip(ctx, tripID)
	}

	if vehicle != nil && vehicle.Trip != nil {
		vehicleID = vehicle.ID.ID
		predicted = true
	}

	status, statusErr := api.BuildTripStatus(ctx, route.AgencyID, tripID, serviceDate, currentTime)
	if statusErr != nil {
		api.Logger.Warn("BuildTripStatus failed",
			"tripID", tripID, "error", statusErr)
	}
	if status != nil {
		tripStatus = status

		predictedArrivalTime = scheduledArrivalTimeMs
		predictedDepartureTime = scheduledDepartureTimeMs

		// getPredictedTimes now returns 3 values (arr, dep, isPredicted)
		// and includes trip-level Delay fallback for consistency with the plural handler
		predictedArrival, predictedDeparture, isPredicted := api.getPredictedTimes(tripID, stopCode, targetStopTime.StopSequence, scheduledArrivalTime, scheduledDepartureTime)

		if isPredicted {
			predictedArrivalTime = predictedArrival
			predictedDepartureTime = predictedDeparture
			predicted = true
		} else {
			predicted = false
		}

		if vehicle != nil && vehicle.Position != nil {
			distanceFromStop = api.getBlockDistanceToStop(ctx, tripID, stopCode, vehicle, serviceDate)

			numberOfStopsAwayPtr := api.getNumberOfStopsAway(ctx, tripID, int(targetStopTime.StopSequence), vehicle, serviceDate)
			if numberOfStopsAwayPtr != nil {
				numberOfStopsAway = *numberOfStopsAwayPtr
			} else {
				numberOfStopsAway = -1
			}
		}
	}

	totalStopsInTrip := len(stopTimes)

	blockTripSequence := api.calculateBlockTripSequence(ctx, tripID, serviceDate)

	lastUpdateTime := api.GtfsManager.GetVehicleLastUpdateTime(vehicle)
	var lastUpdateTimePtr *int64
	if lastUpdateTime > 0 {
		lastUpdateTimePtr = utils.Int64Ptr(lastUpdateTime)
	}

	situationIDs := api.GetSituationIDsForTrip(r.Context(), tripID)

	arrival := models.NewArrivalAndDeparture(
		utils.FormCombinedID(route.AgencyID, route.ID), // routeID
		route.ShortName.String,                         // routeShortName
		route.LongName.String,                          // routeLongName
		utils.FormCombinedID(route.AgencyID, tripID),   // tripID
		trip.TripHeadsign.String,                       // tripHeadsign
		stopID,                                         // stopID
		vehicleID,                                      // vehicleID
		serviceDateMillis,                              // serviceDate
		scheduledArrivalTimeMs,                         // scheduledArrivalTime
		scheduledDepartureTimeMs,                       // scheduledDepartureTime
		predictedArrivalTime,                           // predictedArrivalTime
		predictedDepartureTime,                         // predictedDepartureTime
		lastUpdateTimePtr,                              // lastUpdateTime
		predicted,                                      // predicted
		true,                                           // arrivalEnabled
		true,                                           // departureEnabled
		int(targetStopTime.StopSequence)-1,             // stopSequence (Zero-based index)
		totalStopsInTrip,                               // totalStopsInTrip
		numberOfStopsAway,                              // numberOfStopsAway
		blockTripSequence,                              // blockTripSequence
		distanceFromStop,                               // distanceFromStop
		"default",                                      // status
		"",                                             // occupancyStatus
		"",                                             // predictedOccupancy
		"",                                             // historicalOccupancy
		tripStatus,                                     // tripStatus
		situationIDs,                                   // situationIds
	)

	references := models.NewEmptyReferences()

	// Add Stop Agency Reference
	references.Agencies = append(references.Agencies, models.NewAgencyReference(
		stopAgency.ID,
		stopAgency.Name,
		stopAgency.Url,
		stopAgency.Timezone,
		stopAgency.Lang.String,
		stopAgency.Phone.String,
		stopAgency.Email.String,
		stopAgency.FareUrl.String,
		"",
		false,
	))

	// Add Route Agency Reference if different from Stop Agency
	if route.AgencyID != stopAgency.ID {
		routeAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
		if err == nil {
			references.Agencies = append(references.Agencies, models.NewAgencyReference(
				routeAgency.ID,
				routeAgency.Name,
				routeAgency.Url,
				routeAgency.Timezone,
				routeAgency.Lang.String,
				routeAgency.Phone.String,
				routeAgency.Email.String,
				routeAgency.FareUrl.String,
				"",
				false,
			))
		} else {
			api.Logger.Warn("failed to fetch route agency for reference", "agencyID", route.AgencyID, "error", err)
		}
	}

	tripRef := models.NewTripReference(
		utils.FormCombinedID(route.AgencyID, tripID),
		utils.FormCombinedID(route.AgencyID, trip.RouteID),
		utils.FormCombinedID(route.AgencyID, trip.ServiceID),
		trip.TripHeadsign.String,
		"", // trip short name
		strconv.FormatInt(trip.DirectionID.Int64, 10),
		utils.FormCombinedID(route.AgencyID, trip.BlockID.String),
		utils.FormCombinedID(route.AgencyID, trip.ShapeID.String),
	)
	references.Trips = append(references.Trips, *tripRef)

	// Include active trip if it's different from the parameter trip and trip status is not null
	if tripStatus != nil && tripStatus.ActiveTripID != "" {
		_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(tripStatus.ActiveTripID)
		if err == nil && activeTripID != tripID {
			activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID)
			if err == nil {
				activeRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, activeTrip.RouteID)
				if err != nil {
					api.Logger.Warn("failed to fetch route for active trip reference", "tripID", activeTripID, "error", err)
				} else {
					activeTripRef := models.NewTripReference(
						utils.FormCombinedID(activeRoute.AgencyID, activeTripID),
						utils.FormCombinedID(activeRoute.AgencyID, activeTrip.RouteID),
						utils.FormCombinedID(activeRoute.AgencyID, activeTrip.ServiceID),
						activeTrip.TripHeadsign.String,
						"", // trip short name
						strconv.FormatInt(activeTrip.DirectionID.Int64, 10),
						utils.FormCombinedID(activeRoute.AgencyID, activeTrip.BlockID.String),
						utils.FormCombinedID(activeRoute.AgencyID, activeTrip.ShapeID.String),
					)
					references.Trips = append(references.Trips, *activeTripRef)
				}
			}
		}
	}

	// Build stops references
	stopIDSet := make(map[string]bool)
	routeIDSet := make(map[string]*gtfsdb.Route)

	stopIDSet[stop.ID] = true

	// Include the next and closest stops if trip status is not null to stops reference
	if tripStatus != nil {
		if tripStatus.NextStop != "" {
			_, nextStopID, err := utils.ExtractAgencyIDAndCodeID(tripStatus.NextStop)

			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}

			stopIDSet[nextStopID] = true
		}
		if tripStatus.ClosestStop != "" {
			_, closestStopID, err := utils.ExtractAgencyIDAndCodeID(tripStatus.ClosestStop)

			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDSet[closestStopID] = true
		}
	}
	// batch fetch
	stopIDsSlice := make([]string, 0, len(stopIDSet))
	for sid := range stopIDSet {
		stopIDsSlice = append(stopIDsSlice, sid)
	}

	batchedStops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDsSlice)
	if err != nil {
		api.Logger.Warn("failed to batch fetch stops for references", "error", err)
		batchedStops = nil
	}

	stopDataMap := make(map[string]gtfsdb.Stop)
	for _, s := range batchedStops {
		stopDataMap[s.ID] = s
	}

	batchedRoutesForStops, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDsSlice)
	if err != nil {
		api.Logger.Warn("failed to batch fetch routes for stops", "error", err)
		batchedRoutesForStops = nil
	}

	routesByStopID := make(map[string][]gtfsdb.GetRoutesForStopsRow)
	for _, routeRow := range batchedRoutesForStops {
		routesByStopID[routeRow.StopID] = append(routesByStopID[routeRow.StopID], routeRow)
	}

	for _, sid := range stopIDsSlice {
		stopData, exists := stopDataMap[sid]
		if !exists {
			continue
		}

		routesForThisStop := routesByStopID[sid]
		combinedRouteIDs := make([]string, len(routesForThisStop))
		for i, route := range routesForThisStop {
			combinedRouteIDs[i] = utils.FormCombinedID(route.AgencyID, route.ID)
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

		stopRef := models.Stop{
			ID:                 utils.FormCombinedID(stopAgencyID, stopData.ID),
			Name:               stopData.Name.String,
			Lat:                stopData.Lat,
			Lon:                stopData.Lon,
			Code:               stopData.Code.String,
			Direction:          api.DirectionCalculator.CalculateStopDirection(r.Context(), stopData.ID, stopData.Direction),
			LocationType:       int(stopData.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stopData.WheelchairBoarding)),
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		references.Stops = append(references.Stops, stopRef)
	}

	// Build routes references
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
	}

	if len(situationIDs) > 0 {
		alerts := api.GtfsManager.GetAlertsForTrip(r.Context(), tripID)
		if len(alerts) > 0 {
			situations := api.BuildSituationReferences(alerts)
			references.Situations = append(references.Situations, situations...)
		}
	}

	response := models.NewEntryResponse(arrival, references, api.Clock)
	api.sendResponse(w, r, response)
}

// getPredictedTimes computes predicted arrival/departure times from GTFS-RT TripUpdate data.
// It implements a 3-tier fallback strategy matching the plural handler's behavior:
//  1. Exact stop match — uses per-stop arrival/departure time or delay directly
//  2. Propagated delay — uses delay from the closest prior stop in the trip
//  3. Trip-level delay — falls back to TripUpdate.Delay when no per-stop data exists
//
// Returns (predictedArrivalMs, predictedDepartureMs, isPredicted).
// Returns (0, 0, false) if no prediction can be made.
func (api *RestAPI) getPredictedTimes(
	tripID string,
	stopCode string,
	targetStopSequence int64,
	scheduledArrivalTime, scheduledDepartureTime time.Time,
) (predictedArrivalTime, predictedDepartureTime int64, predicted bool) {

	realTimeTrip, _ := api.GtfsManager.GetTripUpdateByID(tripID)
	// trip-level delay exists but StopTimeUpdates is empty
	if realTimeTrip == nil || (len(realTimeTrip.StopTimeUpdates) == 0) && realTimeTrip.Delay == nil {
		return 0, 0, false
	}

	var arrivalOffset, departureOffset *int64
	var propagatedDelay int64 = 0
	var closestPriorSequence int64 = -1
	var foundTarget bool

	for _, stu := range realTimeTrip.StopTimeUpdates {
		seq := int64(-1)
		if stu.StopSequence != nil {
			seq = int64(*stu.StopSequence)
		}

		if (stu.StopID != nil && *stu.StopID == stopCode) || (seq != -1 && seq == targetStopSequence) {
			foundTarget = true
			if stu.Arrival != nil {
				if stu.Arrival.Time != nil {
					offset := stu.Arrival.Time.Sub(scheduledArrivalTime).Nanoseconds()
					arrivalOffset = &offset
				} else if stu.Arrival.Delay != nil {
					offset := int64(*stu.Arrival.Delay)
					arrivalOffset = &offset
				}
			}
			if stu.Departure != nil {
				if stu.Departure.Time != nil {
					offset := stu.Departure.Time.Sub(scheduledDepartureTime).Nanoseconds()
					departureOffset = &offset
				} else if stu.Departure.Delay != nil {
					offset := int64(*stu.Departure.Delay)
					departureOffset = &offset
				}
			}
			break
		}

		if seq != -1 && seq < targetStopSequence && seq > closestPriorSequence {
			closestPriorSequence = seq
			propagatedDelay = 0
			if stu.Departure != nil && stu.Departure.Delay != nil {
				propagatedDelay = int64(*stu.Departure.Delay)
			} else if stu.Arrival != nil && stu.Arrival.Delay != nil {
				propagatedDelay = int64(*stu.Arrival.Delay)
			}
		}
	}

	// CHANGED: Restructured fallback chain to include trip-level Delay (Tier 3)
	// Previously this returned (0, 0) when !foundTarget && closestPriorSequence == -1,
	// ignoring trip-level delay entirely
	if !foundTarget {
		if closestPriorSequence != -1 {
			// Fallback 1: Propagated delay from closest prior stop
			arr := propagatedDelay
			dep := propagatedDelay
			arrivalOffset = &arr
			departureOffset = &dep
		} else if realTimeTrip.Delay != nil {
			// Fallback 2: Trip-level delay — matches plural handler behavior
			delayNs := realTimeTrip.Delay.Nanoseconds()
			arrivalOffset = &delayNs
			departureOffset = &delayNs
		} else {
			return 0, 0, false
		}
	}

	if arrivalOffset == nil {
		arrivalOffset = &propagatedDelay
	}
	if departureOffset == nil {
		departureOffset = &propagatedDelay
	}

	// Rule 1: arrival == departure (Simplified Logic)
	if scheduledArrivalTime.Equal(scheduledDepartureTime) {
		offset := *arrivalOffset

		if *departureOffset != propagatedDelay && *departureOffset != *arrivalOffset {
			offset = *departureOffset
		}

		predictedArrival := scheduledArrivalTime.Add(time.Duration(offset))
		predictedDeparture := scheduledDepartureTime.Add(time.Duration(offset))
		return predictedArrival.UnixMilli(), predictedDeparture.UnixMilli(), true
	}

	// Rule 2: arrival < departure
	predictedArrival := scheduledArrivalTime.Add(time.Duration(*arrivalOffset))
	predictedDeparture := scheduledDepartureTime.Add(time.Duration(*departureOffset))

	return predictedArrival.UnixMilli(), predictedDeparture.UnixMilli(), true
}

func (api *RestAPI) getNumberOfStopsAway(ctx context.Context, targetTripID string, targetStopSequence int, vehicle *gtfs.Vehicle, serviceDate time.Time) *int {
	currentVehicleStopSequence := getCurrentVehicleStopSequence(vehicle)
	if currentVehicleStopSequence == nil {
		return nil
	}

	activeTripID := GetVehicleActiveTripID(vehicle)
	if activeTripID == "" {
		activeTripID = targetTripID
	}

	targetGlobalSeq := api.getBlockSequenceForStopSequence(ctx, targetTripID, targetStopSequence, serviceDate)
	vehicleGlobalSeq := api.getBlockSequenceForStopSequence(ctx, activeTripID, *currentVehicleStopSequence, serviceDate)

	numberOfStopsAway := targetGlobalSeq - vehicleGlobalSeq - 1
	return &numberOfStopsAway
}
