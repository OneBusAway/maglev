package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) arrivalsAndDeparturesForStopHandler(w http.ResponseWriter, r *http.Request) {
	stopID := utils.ExtractIDFromParams(r)
	agencyID, stopCode, err := utils.ExtractAgencyIDAndCodeID(stopID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	params := ArrivalAndDepartureParams{
		MinutesAfter:  35,
		MinutesBefore: 5,
	}

	if minutesAfterStr := r.URL.Query().Get("minutesAfter"); minutesAfterStr != "" {
		if minutesAfter, err := strconv.Atoi(minutesAfterStr); err == nil {
			params.MinutesAfter = minutesAfter
		}
	}
	if minutesBeforeStr := r.URL.Query().Get("minutesBefore"); minutesBeforeStr != "" {
		if minutesBefore, err := strconv.Atoi(minutesBeforeStr); err == nil {
			params.MinutesBefore = minutesBefore
		}
	}

	var currentTime time.Time
	if timeStr := r.URL.Query().Get("time"); timeStr != "" {
		if timeMs, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
			currentTime = time.Unix(timeMs/1000, 0)
		}
	} else {
		currentTime = api.Clock.Now()
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
	currentTime = currentTime.In(loc)
	windowStart := currentTime.Add(-time.Duration(params.MinutesBefore) * time.Minute)
	windowEnd := currentTime.Add(time.Duration(params.MinutesAfter) * time.Minute)

	windowStartNanos := convertToNanosSinceMidnight(windowStart)
	windowEndNanos := convertToNanosSinceMidnight(windowEnd)

	serviceDate := currentTime.Format("20060102")
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
			stopTimes = append(stopTimes, gtfsdb.GetStopTimesForStopInWindowRow{
				TripID:        st.TripID,
				ArrivalTime:   st.ArrivalTime,
				DepartureTime: st.DepartureTime,
				StopSequence:  st.StopSequence,
				RouteID:       st.RouteID,
				ServiceID:     st.ServiceID,
				TripHeadsign:  st.TripHeadsign,
				BlockID:       st.BlockID,
			})
		}
	}

	tripIDSet := make(map[string]*gtfsdb.Trip)
	routeIDSet := make(map[string]*gtfsdb.Route)
	stopIDSet := make(map[string]bool)

	// Add the current stop
	stopIDSet[stop.ID] = true

	for _, st := range stopTimes {
		route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, st.RouteID)
		if err != nil {
			continue
		}

		trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, st.TripID)
		if err != nil {
			continue
		}

		routeIDSet[route.ID] = &route
		tripIDSet[trip.ID] = &trip

		serviceDateMillis := currentTime.UnixMilli()

		serviceMidnight := time.Date(
			currentTime.Year(),
			currentTime.Month(),
			currentTime.Day(),
			0, 0, 0, 0,
			loc,
		)

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

			// Check if we have stop time updates for this trip
			if len(vehicle.Trip.StopTimeUpdates) > 0 {
				// Look for StopTimeUpdate that matches this stop
				for _, stopTimeUpdate := range vehicle.Trip.StopTimeUpdates {
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
			status, _ := api.BuildTripStatus(ctx, agencyID, st.TripID, currentTime, currentTime)
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
					distanceFromStop = api.getBlockDistanceToStop(ctx, st.TripID, stopCode, vehicle, currentTime)

					numberOfStopsAwayPtr := api.getNumberOfStopsAway(ctx, st.TripID, int(st.StopSequence), vehicle, currentTime)
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
						activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID)
						if err == nil {
							tripIDSet[activeTrip.ID] = &activeTrip
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
		totalStopsInTrip := len(tripStopTimes)
		if err != nil {
			totalStopsInTrip = 0
		}

		blockTripSequence := api.calculateBlockTripSequence(ctx, st.TripID, currentTime)

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
			currentTime.UnixMilli(),                   // lastUpdateTime
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
			api.GetSituationIDsForTrip(st.TripID),     // situationIDs
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

	for stopID := range stopIDSet {
		stopData, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
		if err != nil {
			continue
		}

		routesForThisStop, _ := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, []string{stopID})
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
			Direction:          api.calculateStopDirection(ctx, stopID),
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
			nearbyStopIDs = []string{utils.FormCombinedID(agencyID, s.ID)}
			break
		}
	}
	return nearbyStopIDs
}
