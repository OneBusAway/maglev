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
	loc, _ := time.LoadLocation(agency.Timezone)
	if params.Time != nil {
		currentTime = params.Time.In(loc)
	} else {
		currentTime = time.Now().In(loc)
	}

	// Use the provided service date
	serviceDate := *params.ServiceDate
	serviceDateMillis := serviceDate.Unix() * 1000

	// Service date is a "date" only, so get midnight in agency's TZ
	serviceMidnight := time.Date(
		serviceDate.Year(),
		serviceDate.Month(),
		serviceDate.Day(),
		0, 0, 0, 0,
		loc,
	)

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
		}
	} else {
		// If vehicleId is not provided, get the vehicle for the trip
		vehicle = api.GtfsManager.GetVehicleForTrip(tripID)
	}

	if vehicle != nil && vehicle.Trip != nil {
		vehicleID = vehicle.ID.ID
		predicted = true
	}

	status, _ := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	if status != nil {
		tripStatus = status

		predictedArrivalTime = scheduledArrivalTimeMs
		predictedDepartureTime = scheduledDepartureTimeMs

		predictedArrival, predictedDeparture := api.getPredictedTimes(tripID, stopCode, scheduledArrivalTime, scheduledDepartureTime)

		if predictedArrival != 0 && predictedDeparture != 0 {
			predictedArrivalTime = predictedArrival
			predictedDepartureTime = predictedDeparture
			predicted = true
		} else {
			predicted = false
		}

		if vehicle != nil && vehicle.Position != nil {
			// Calculate remaining distance along the trip shape to this stop
			if d := api.getRemainingDistanceToStop(ctx, tripID, stopCode, vehicle); d != nil {
				distanceFromStop = *d
			} else {
				distanceFromStop = 0
			}
			numberOfStopsAwayPtr := getNumberOfStopsAway(int(targetStopTime.StopSequence), vehicle)

			if numberOfStopsAwayPtr != nil {
				numberOfStopsAway = *numberOfStopsAwayPtr
			} else {
				numberOfStopsAway = -1
			}

		}
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
		getLastUpdateTime(vehicle),
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

	// Include active trip if it's different from the parameter trip and trip status is not null
	if tripStatus != nil && tripStatus.ActiveTripID != "" {
		_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(tripStatus.ActiveTripID)
		if err == nil && activeTripID != tripID {
			activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID)
			if err == nil {
				activeTripRef := models.NewTripReference(
					utils.FormCombinedID(agencyID, activeTripID),
					utils.FormCombinedID(agencyID, activeTrip.RouteID),
					utils.FormCombinedID(agencyID, activeTrip.ServiceID),
					activeTrip.TripHeadsign.String,
					"", // trip short name
					activeTrip.DirectionID.Int64,
					utils.FormCombinedID(agencyID, activeTrip.BlockID.String),
					utils.FormCombinedID(agencyID, activeTrip.ShapeID.String),
				)
				references.Trips = append(references.Trips, activeTripRef)
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

func (api *RestAPI) getPredictedTimes(
	tripID string,
	stopCode string,
	scheduledArrivalTime, scheduledDepartureTime time.Time,
) (predictedArrivalTime, predictedDepartureTime int64) {

	realTimeTrip, _ := api.GtfsManager.GetTripUpdateByID(tripID)
	if realTimeTrip == nil || len(realTimeTrip.StopTimeUpdates) == 0 {
		return 0, 0
	}

	var arrivalOffset, departureOffset *int64

	for _, stu := range realTimeTrip.StopTimeUpdates {
		if stu.StopID != nil && *stu.StopID == stopCode {

			if stu.Arrival != nil && stu.Arrival.Time != nil {
				offset := stu.Arrival.Time.Sub(scheduledArrivalTime).Nanoseconds()
				arrivalOffset = &offset
			}
			if stu.Departure != nil && stu.Departure.Time != nil {
				offset := stu.Departure.Time.Sub(scheduledDepartureTime).Nanoseconds()
				departureOffset = &offset
			}
			break
		}
	}

	if arrivalOffset == nil && departureOffset == nil {
		return 0, 0
	}

	// Rule 1: arrival == departure → copy whichever delay exists to both
	if scheduledArrivalTime.Equal(scheduledDepartureTime) {
		var offset int64
		if arrivalOffset != nil {
			offset = *arrivalOffset
		} else {
			offset = *departureOffset
		}

		predictedArrival := scheduledArrivalTime.Add(time.Duration(offset))
		predictedDeparture := scheduledDepartureTime.Add(time.Duration(offset))
		return predictedArrival.UnixMilli(), predictedDeparture.UnixMilli()
	}

	// Rule 2: arrival < departure
	var predictedArrival, predictedDeparture time.Time

	if arrivalOffset != nil {
		predictedArrival = scheduledArrivalTime.Add(time.Duration(*arrivalOffset))
	} else {
		predictedArrival = scheduledArrivalTime
	}

	if departureOffset != nil {
		predictedDeparture = scheduledDepartureTime.Add(time.Duration(*departureOffset))
	} else {
		predictedDeparture = scheduledDepartureTime
	}

	return predictedArrival.UnixMilli(), predictedDeparture.UnixMilli()
}

func (api *RestAPI) getRemainingDistanceToStop(ctx context.Context, tripID string, stopID string, vehicle *gtfs.Vehicle) *float64 {
	if vehicle == nil || vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
		return nil
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if err != nil || len(shapeRows) < 2 {
		stop, e := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
		if e != nil {
			return nil
		}
		d := utils.Haversine(float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude), stop.Lat, stop.Lon)
		return &d
	}

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
	}

	vehicleAlong := getDistanceAlongShape(float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude), shapePoints)

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		return nil
	}
	stopAlong := getDistanceAlongShape(stop.Lat, stop.Lon, shapePoints)

	remaining := stopAlong - vehicleAlong

	return &remaining
}

func getNumberOfStopsAway(targetStopSequence int, vehicle *gtfs.Vehicle) *int {
	currentVehicleStopSequence := getCurrentVehicleStopSequence(vehicle)
	if currentVehicleStopSequence == nil {
		return nil
	}

	numberOfStopsAway := targetStopSequence - *currentVehicleStopSequence - 1
	return &numberOfStopsAway
}

func getCurrentVehicleStopSequence(vehicle *gtfs.Vehicle) *int {
	if vehicle == nil || vehicle.CurrentStopSequence == nil {
		return nil
	}

	val := int(*vehicle.CurrentStopSequence)
	return &val
}

func getLastUpdateTime(vehicle *gtfs.Vehicle) int64 {
	if vehicle == nil || vehicle.Timestamp == nil {
		return 0
	}
	return vehicle.Timestamp.UnixMilli()
}
