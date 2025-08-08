package restapi

import (
	"net/http"
	"strconv"
	"time"

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

func (api *RestAPI) parseArrivalAndDepartureParams(r *http.Request) ArrivalAndDepartureParams {
	params := ArrivalAndDepartureParams{
		MinutesAfter:  30, // Default 30 minutes after
		MinutesBefore: 5,  // Default 5 minutes before
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

	if timeStr := r.URL.Query().Get("time"); timeStr != "" {
		if timeMs, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
			timeParam := time.Unix(timeMs/1000, 0)
			params.Time = &timeParam
		}
	}

	// Required tripId parameter
	if tripIDStr := r.URL.Query().Get("tripId"); tripIDStr != "" {
		params.TripID = tripIDStr
	}

	// Required serviceDate parameter
	if serviceDateStr := r.URL.Query().Get("serviceDate"); serviceDateStr != "" {
		if serviceDateMs, err := strconv.ParseInt(serviceDateStr, 10, 64); err == nil {
			serviceDate := time.Unix(serviceDateMs/1000, 0)
			params.ServiceDate = &serviceDate
		}
	}

	// Optional vehicleId parameter
	if vehicleIDStr := r.URL.Query().Get("vehicleId"); vehicleIDStr != "" {
		params.VehicleID = vehicleIDStr
	}

	// Optional stopSequence parameter
	if stopSequenceStr := r.URL.Query().Get("stopSequence"); stopSequenceStr != "" {
		if stopSequence, err := strconv.Atoi(stopSequenceStr); err == nil {
			params.StopSequence = &stopSequence
		}
	}

	return params
}

func (api *RestAPI) arrivalAndDepartureForStopHandler(w http.ResponseWriter, r *http.Request) {
	stopID := utils.ExtractIDFromParams(r)

	agencyID, stopCode, err := utils.ExtractAgencyIDAndCodeID(stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	ctx := r.Context()
	params := api.parseArrivalAndDepartureParams(r)

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
		api.serverErrorResponse(w, r, err)
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
		loc, _ := time.LoadLocation(agency.Timezone)
		currentTime = params.Time.In(loc)
	} else {
		loc, _ := time.LoadLocation(agency.Timezone)
		currentTime = time.Now().In(loc)
	}

	// Use the provided service date
	serviceDate := *params.ServiceDate
	serviceDateMillis := serviceDate.Unix() * 1000

	// Calculate actual timestamps for arrival and departure
	startOfDay := serviceDate.Truncate(24 * time.Hour)
	scheduledArrivalTimeMs := startOfDay.Add(time.Duration(targetStopTime.ArrivalTime)).UnixMilli()
	scheduledDepartureTimeMs := startOfDay.Add(time.Duration(targetStopTime.DepartureTime)).UnixMilli()

	// Get real-time data for this trip if available
	var predictedArrivalTime, predictedDepartureTime int64
	var predicted bool
	var vehicleID string
	var tripStatus *models.TripStatusForTripDetails
	var distanceFromStop float64
	var numberOfStopsAway int

	// If vehicleId is provided, validate it matches the trip
	if params.VehicleID != "" {
		_, providedVehicleID, err := utils.ExtractAgencyIDAndCodeID(params.VehicleID)
		if err == nil {
			vehicle, err := api.GtfsManager.GetVehicleByID(providedVehicleID)
			if err == nil && vehicle != nil && vehicle.Trip != nil && vehicle.Trip.ID.ID == tripID {
				vehicleID = vehicle.ID.ID
				predicted = true

				// Build trip status
				status, _ := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
				if status != nil {
					tripStatus = status
					predictedArrivalTime = scheduledArrivalTimeMs
					predictedDepartureTime = scheduledDepartureTimeMs

					if vehicle.Position != nil {
						distanceFromStop = 0  // TODO: Calculate actual distance
						numberOfStopsAway = 0 // TODO: Calculate actual number of stops away
					}
				}
			}
		}
	} else {
		vehicle := api.GtfsManager.GetVehicleForTrip(tripID)
		if vehicle != nil && vehicle.Trip != nil {
			vehicleID = vehicle.ID.ID
			predicted = true

			status, _ := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
			if status != nil {
				tripStatus = status
				predictedArrivalTime = scheduledArrivalTimeMs
				predictedDepartureTime = scheduledDepartureTimeMs

				if vehicle.Position != nil {
					distanceFromStop = 100.0 // TODO: Calculate actual distance
					numberOfStopsAway = 2    // TODO: Calculate actual number of stops away
				}
			}
		}
	}

	if !predicted {
		predictedArrivalTime = 0
		predictedDepartureTime = 0
	}

	totalStopsInTrip := len(stopTimes)

	blockTripSequence := api.calculateBlockTripSequence(ctx, tripID, serviceDate)

	arrival := models.NewArrivalAndDeparture(
		utils.FormCombinedID(agencyID, route.ID),
		route.ShortName.String,
		route.LongName.String,
		utils.FormCombinedID(agencyID, tripID),
		trip.TripHeadsign.String,
		stopID,
		vehicleID,
		serviceDateMillis,
		scheduledArrivalTimeMs,
		scheduledDepartureTimeMs,
		predictedArrivalTime,
		predictedDepartureTime,
		currentTime.UnixMilli(),
		predicted,
		true,                               // arrivalEnabled
		true,                               // departureEnabled
		int(targetStopTime.StopSequence)-1, // Zero-based index
		totalStopsInTrip,
		numberOfStopsAway,
		blockTripSequence,
		distanceFromStop,
		"default", // status
		"",        // occupancyStatus
		"",        // predictedOccupancy
		"",        // historicalOccupancy
		tripStatus,
		[]string{},
	)

	references := models.NewEmptyReferences()

	references.Agencies = append(references.Agencies, models.NewAgencyReference(
		agency.ID,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Lang.String,
		agency.Phone.String,
		agency.Email.String,
		agency.FareUrl.String,
		"",
		false,
	))

	tripRef := models.NewTripReference(
		utils.FormCombinedID(agencyID, tripID),
		utils.FormCombinedID(agencyID, trip.RouteID),
		utils.FormCombinedID(agencyID, trip.ServiceID),
		trip.TripHeadsign.String,
		"", // trip short name
		trip.DirectionID.Int64,
		utils.FormCombinedID(agencyID, trip.BlockID.String),
		utils.FormCombinedID(agencyID, trip.ShapeID.String),
	)
	references.Trips = append(references.Trips, tripRef)

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
			}

			stopIDSet[nextStopID] = true
		}
		if tripStatus.ClosestStop != "" {
			_, closestStopID, err := utils.ExtractAgencyIDAndCodeID(tripStatus.ClosestStop)

			if err != nil {
				api.serverErrorResponse(w, r, err)
			}
			stopIDSet[closestStopID] = true
		}
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
			ID:                 utils.FormCombinedID(agencyID, stopData.ID),
			Name:               stopData.Name.String,
			Lat:                stopData.Lat,
			Lon:                stopData.Lon,
			Code:               stopData.Code.String,
			Direction:          "N", // TODO: Calculate actual direction
			LocationType:       int(stopData.LocationType.Int64),
			WheelchairBoarding: "UNKNOWN",
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		references.Stops = append(references.Stops, stopRef)
	}

	// Build routes references
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

	response := models.NewEntryResponse(arrival, references)
	api.sendResponse(w, r, response)
}
