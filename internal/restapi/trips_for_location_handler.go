package restapi

import (
	"context"
	"github.com/jamespfennell/gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
	"math"
	"net/http"
	"time"
)

func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, err := api.parseAndValidateRequest(w, r)
	if err != nil {
		return
	}

	stops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, -1, latSpan, lonSpan, "", 100, false)
	stopIDs := extractStopIDs(stops)
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesByStopIDs(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	activeTrips := api.getActiveTrips(stopTimes, api.GtfsManager.GetRealTimeVehicles())
	bbox := boundingBox(lat, lon, latSpan, lonSpan)

	allRoutes, allTrips, err := api.getAllRoutesAndTrips(ctx, w, r)
	if err != nil {
		return
	}
	tripAgencyResolver := NewTripAgencyResolver(allRoutes, allTrips)

	result := api.buildTripsForLocationEntries(ctx, activeTrips, bbox, tripAgencyResolver, includeSchedule, currentLocation, todayMidnight, serviceDate, w, r)
	references := api.BuildReference(ctx, includeTrip, allRoutes, allTrips, result)
	response := models.NewListResponseWithRange(result, references, len(result) == 0)
	api.sendResponse(w, r, response)
}

func (api *RestAPI) parseAndValidateRequest(w http.ResponseWriter, r *http.Request) (lat, lon, latSpan, lonSpan float64, includeTrip, includeSchedule bool, currentLocation *time.Location, todayMidnight time.Time, serviceDate time.Time, err error) {
	queryParams := r.URL.Query()
	lat, fieldErrors := utils.ParseFloatParam(queryParams, "lat", nil)
	lon, _ = utils.ParseFloatParam(queryParams, "lon", fieldErrors)
	latSpan, _ = utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, _ = utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)
	includeTrip = queryParams.Get("includeTrip") == "true"
	includeSchedule = queryParams.Get("includeSchedule") == "true"

	currentAgency := api.GtfsManager.GetAgencies()[0]
	currentLocation, _ = time.LoadLocation(currentAgency.Timezone)
	timeParam := queryParams.Get("time")
	currentTime := time.Now().In(currentLocation)
	todayMidnight = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	_, serviceDate, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)

	ctx := r.Context()
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		err = ctx.Err()
		return
	}
	if !success || len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		err = ctx.Err()
		return
	}
	locationErrors := utils.ValidateLocationParams(lat, lon, 0, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		err = ctx.Err()
		return
	}
	return
}

func extractStopIDs(stops []*gtfs.Stop) []string {
	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.Id
	}
	return stopIDs
}

func (api *RestAPI) getActiveTrips(stopTimes []gtfsdb.StopTime, realTimeVehicles []gtfs.Vehicle) map[string]gtfs.Vehicle {
	trips := make(map[string]bool)
	for _, stopTime := range stopTimes {
		trips[stopTime.TripID] = true
	}
	activeTrips := make(map[string]gtfs.Vehicle)
	for _, vehicle := range realTimeVehicles {
		if vehicle.Trip != nil && trips[vehicle.Trip.ID.ID] {
			activeTrips[vehicle.Trip.ID.ID] = vehicle
		}
	}
	return activeTrips
}

type boundingBoxStruct struct{ minLat, maxLat, minLon, maxLon float64 }

func boundingBox(lat, lon, latSpan, lonSpan float64) boundingBoxStruct {
	const epsilon = 1e-6
	return boundingBoxStruct{
		minLat: lat - latSpan - epsilon,
		maxLat: lat + latSpan + epsilon,
		minLon: lon - lonSpan - epsilon,
		maxLon: lon + lonSpan + epsilon,
	}
}

func (api *RestAPI) getAllRoutesAndTrips(ctx context.Context, w http.ResponseWriter, r *http.Request) ([]gtfsdb.Route, []gtfsdb.Trip, error) {
	allRoutes, err := api.GtfsManager.GtfsDB.Queries.ListRoutes(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil, nil, err
	}
	allTrips, err := api.GtfsManager.GtfsDB.Queries.ListTrips(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil, nil, err
	}
	return allRoutes, allTrips, nil
}

func (api *RestAPI) buildTripsForLocationEntries(
	ctx context.Context,
	activeTrips map[string]gtfs.Vehicle,
	bbox boundingBoxStruct,
	tripAgencyResolver *TripAgencyResolver,
	includeSchedule bool,
	currentLocation *time.Location,
	todayMidnight time.Time,
	serviceDate time.Time,
	w http.ResponseWriter,
	r *http.Request,
) []models.TripsForLocationListEntry {
	var result []models.TripsForLocationListEntry
	for _, vehicle := range activeTrips {
		pos := vehicle.Position
		if pos == nil {
			continue
		}
		lat, lon := float64(*pos.Latitude), float64(*pos.Longitude)
		if lat < bbox.minLat || lat > bbox.maxLat || lon < bbox.minLon || lon > bbox.maxLon {
			continue
		}
		tripID := vehicle.Trip.ID.ID
		agencyID := tripAgencyResolver.GetAgencyNameByTripID(tripID)
		var schedule *models.TripsForLocationSchedule
		if includeSchedule {
			schedule = api.buildScheduleForTrip(ctx, tripID, agencyID, serviceDate, currentLocation, w, r)
			if schedule == nil {
				continue
			}
		}
		entry := models.TripsForLocationListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(tripID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}
	return result
}

func (api *RestAPI) buildScheduleForTrip(
	ctx context.Context,
	tripID, agencyID string, serviceDate time.Time,
	currentLocation *time.Location,
	w http.ResponseWriter,
	r *http.Request,
) *models.TripsForLocationSchedule {
	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	var shapePoints []gtfs.ShapePoint
	if err == nil && len(shapeRows) > 1 {
		shapePoints = make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
		}
	}
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	nextTripID, previousTripID, stopTimes, err := api.GetNextAndPreviousTripIDs(ctx, &trip, tripID, agencyID, serviceDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil
	}
	stopTimesList := buildStopTimesList(api, ctx, stopTimes, shapePoints, agencyID)
	return &models.TripsForLocationSchedule{
		Frequency:      nil,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}
}

func buildStopTimesList(api *RestAPI, ctx context.Context, stopTimes []gtfsdb.StopTime, shapePoints []gtfs.ShapePoint, agencyID string) []models.StopTime {
	// Precompute cumulative distances along the shape
	cumDist := make([]float64, len(shapePoints))
	for i := 1; i < len(shapePoints); i++ {
		cumDist[i] = cumDist[i-1] + utils.Haversine(
			shapePoints[i-1].Latitude, shapePoints[i-1].Longitude,
			shapePoints[i].Latitude, shapePoints[i].Longitude,
		)
	}
	stopTimesList := make([]models.StopTime, 0, len(stopTimes))
	for _, stopTime := range stopTimes {
		stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopTime.StopID)
		if err != nil {
			continue
		}
		stopLat, stopLon := stop.Lat, stop.Lon
		minIdx, minDist := 0, math.MaxFloat64
		for i, sp := range shapePoints {
			d := utils.Haversine(stopLat, stopLon, sp.Latitude, sp.Longitude)
			if d < minDist {
				minDist = d
				minIdx = i
			}
		}
		distanceAlongTheTrip := cumDist[minIdx]
		stopTimesList = append(stopTimesList, models.StopTime{
			StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
			ArrivalTime:         int(stopTime.ArrivalTime),
			DepartureTime:       int(stopTime.DepartureTime),
			StopHeadsign:        stopTime.StopHeadsign.String,
			DistanceAlongTrip:   distanceAlongTheTrip,
			HistoricalOccupancy: "",
		})
	}
	return stopTimesList
}
func (api *RestAPI) BuildReference(ctx context.Context, includeTrip bool, allRoutes []gtfsdb.Route, allTrips []gtfsdb.Trip, trips []models.TripsForLocationListEntry) models.ReferencesModel {
	// Collect present trip IDs
	presentTrips := make(map[string]models.Trip, len(trips))
	for _, trip := range trips {
		_, tripID, _ := utils.ExtractAgencyIDAndCodeID(trip.TripId)
		presentTrips[tripID] = models.Trip{}
	}

	// Collect present routes and fill presentTrips with details
	presentRoutes := make(map[string]models.Route)
	for _, trip := range allTrips {
		if _, exists := presentTrips[trip.ID]; exists {
			presentTrips[trip.ID] = models.Trip{
				ID:            trip.ID,
				RouteID:       trip.RouteID,
				ServiceID:     trip.ServiceID,
				TripHeadsign:  trip.TripHeadsign.String,
				TripShortName: trip.TripShortName.String,
				DirectionID:   trip.DirectionID.Int64,
				BlockID:       trip.BlockID.String,
				ShapeID:       trip.ShapeID.String,
				PeakOffPeak:   0,
				TimeZone:      "",
			}
			presentRoutes[trip.RouteID] = models.Route{}
		}
	}

	// Collect agencies for present routes
	presentAgencies := make(map[string]models.AgencyReference)
	for _, route := range allRoutes {
		if _, exists := presentRoutes[route.ID]; exists {
			presentRoutes[route.ID] = models.NewRoute(
				utils.FormCombinedID(route.AgencyID, route.ID),
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String,
				route.ShortName.String,
			)
			currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
			if err != nil {
				api.serverErrorResponse(nil, nil, err)
				return models.ReferencesModel{}
			}
			presentAgencies[currentAgency.ID] = models.NewAgencyReference(
				currentAgency.ID,
				currentAgency.Name,
				currentAgency.Url,
				currentAgency.Timezone,
				currentAgency.Lang.String,
				currentAgency.Phone.String,
				currentAgency.Email.String,
				currentAgency.FareUrl.String,
				"",
				false,
			)
		}
	}

	// Optionally include trip details
	tripsRefList := make([]interface{}, 0, len(presentTrips))
	if includeTrip {
		for _, trip := range presentTrips {
			tripDetails, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
			if err == nil {
				var currentAgency = presentRoutes[tripDetails.RouteID].AgencyID
				tripsRefList = append(tripsRefList, models.Trip{
					ID:            utils.FormCombinedID(currentAgency, trip.ID),
					RouteID:       utils.FormCombinedID(currentAgency, tripDetails.RouteID),
					ServiceID:     utils.FormCombinedID(currentAgency, trip.ServiceID),
					TripHeadsign:  tripDetails.TripHeadsign.String,
					TripShortName: tripDetails.TripShortName.String,
					DirectionID:   tripDetails.DirectionID.Int64,
					BlockID:       tripDetails.BlockID.String,
					ShapeID:       utils.FormCombinedID(currentAgency, tripDetails.ShapeID.String),
					PeakOffPeak:   0,
					TimeZone:      "",
				})
			}
		}
	}

	// Convert presentRoutes and presentTrips maps to slices
	routes := make([]interface{}, 0, len(presentRoutes))
	for _, route := range presentRoutes {
		routes = append(routes, route)
	}

	agencyList := make([]models.AgencyReference, 0, len(presentAgencies))
	for _, agency := range presentAgencies {
		agencyList = append(agencyList, agency)
	}

	return models.ReferencesModel{
		Agencies:   agencyList,
		Routes:     routes,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      tripsRefList,
	}
}
