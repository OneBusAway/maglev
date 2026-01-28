package restapi

import (
	"context"
	"net/http"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, err := api.parseAndValidateRequest(w, r)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	stops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, -1, latSpan, lonSpan, "", 100, false, []int{}, api.Clock.Now())
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
		api.serverErrorResponse(w, r, err)
		return
	}
	tripAgencyResolver := NewTripAgencyResolver(allRoutes, allTrips)

	result := api.buildTripsForLocationEntries(ctx, activeTrips, bbox, tripAgencyResolver, includeSchedule, currentLocation, todayMidnight, serviceDate, w, r)
	references := api.BuildReference(w, r, ctx, ReferenceParams{
		IncludeTrip: includeTrip,
		AllRoutes:   allRoutes,
		AllTrips:    allTrips,
		Stops:       stops,
		Trips:       result,
	})
	response := models.NewListResponseWithRange(result, references, len(result) == 0, api.Clock)
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
	currentTime := api.Clock.Now().In(currentLocation)
	todayMidnight = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	_, serviceDate, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)

	ctx := r.Context()
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, ctx.Err()
	}
	if !success || len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, err
	}
	locationErrors := utils.ValidateLocationParams(lat, lon, 0, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, err
	}
	return lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, nil
}

func extractStopIDs(stops []gtfsdb.Stop) []string {
	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.ID
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
		var schedule *models.TripsSchedule
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
			SituationIds: api.GetSituationIDsForTrip(r.Context(),tripID),
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
) *models.TripsSchedule {
	shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	var shapePoints []gtfs.ShapePoint
	if len(shapeRows) > 1 {
		shapePoints = make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
		}
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil
	}

	nextTripID, previousTripID, stopTimes, err := api.GetNextAndPreviousTripIDs(ctx, &trip, agencyID, serviceDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil
	}

	stopTimesList := buildStopTimesList(api, ctx, stopTimes, shapePoints, agencyID)
	return &models.TripsSchedule{
		Frequency:      nil,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}
}

func buildStopTimesList(api *RestAPI, ctx context.Context, stopTimes []gtfsdb.StopTime, shapePoints []gtfs.ShapePoint, agencyID string) []models.StopTime {
	cumulativeDistances := preCalculateCumulativeDistances(shapePoints)

	// Batch-fetch all stop coordinates at once
	stopIDs := make([]string, len(stopTimes))
	for i, st := range stopTimes {
		stopIDs[i] = st.StopID
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)

	// Create a map for quick stop coordinate lookup
	stopCoords := make(map[string]struct{ lat, lon float64 })
	if err != nil {
		// Log the error but continue - distances will be 0 for all stops
		api.Logger.Warn("Failed to batch-fetch stop coordinates for distance calculation",
			"error", err,
			"agency_id", agencyID,
			"stop_count", len(stopIDs))
	} else {
		for _, stop := range stops {
			stopCoords[stop.ID] = struct{ lat, lon float64 }{lat: stop.Lat, lon: stop.Lon}
		}
	}

	stopTimesList := make([]models.StopTime, 0, len(stopTimes))
	for _, stopTime := range stopTimes {
		var distanceAlongTrip float64
		if coords, exists := stopCoords[stopTime.StopID]; exists && len(shapePoints) > 0 {
			distanceAlongTrip = api.calculatePreciseDistanceAlongTripWithCoords(
				coords.lat, coords.lon, shapePoints, cumulativeDistances,
			)
		}

		stopTimesList = append(stopTimesList, models.StopTime{
			StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
			ArrivalTime:         int(stopTime.ArrivalTime),
			DepartureTime:       int(stopTime.DepartureTime),
			StopHeadsign:        utils.NullStringOrEmpty(stopTime.StopHeadsign),
			DistanceAlongTrip:   distanceAlongTrip,
			HistoricalOccupancy: "",
		})
	}
	return stopTimesList
}

type ReferenceParams struct {
	IncludeTrip bool
	AllRoutes   []gtfsdb.Route
	AllTrips    []gtfsdb.Trip
	Stops       []gtfsdb.Stop
	Trips       []models.TripsForLocationListEntry
}

func (api *RestAPI) BuildReference(w http.ResponseWriter, r *http.Request, ctx context.Context, params ReferenceParams) models.ReferencesModel {
	refs := &referenceBuilder{
		api:           api,
		ctx:           ctx,
		presentTrips:  make(map[string]models.Trip, len(params.Trips)),
		presentRoutes: make(map[string]models.Route),
	}

	if err := refs.build(params); err != nil {
		api.serverErrorResponse(w, r, err)
		return models.ReferencesModel{}
	}

	return refs.toReferencesModel()
}

type referenceBuilder struct {
	api             *RestAPI
	ctx             context.Context
	presentTrips    map[string]models.Trip
	presentRoutes   map[string]models.Route
	presentAgencies map[string]models.AgencyReference
	stopList        []models.Stop
	tripsRefList    []interface{}
}

func (rb *referenceBuilder) build(params ReferenceParams) error {
	rb.collectTripIDs(params.Trips)
	rb.buildStopList(params.Stops)
	rb.enrichTripsData(params.AllTrips)

	if err := rb.collectAgenciesAndRoutes(params.AllRoutes); err != nil {
		return err
	}

	if params.IncludeTrip {
		if err := rb.buildTripReferences(); err != nil {
			return err
		}
	}

	return nil
}

func (rb *referenceBuilder) collectTripIDs(trips []models.TripsForLocationListEntry) {
	for _, trip := range trips {
		_, tripID, err := utils.ExtractAgencyIDAndCodeID(trip.TripId)
		if err != nil {
			rb.presentTrips[tripID] = models.Trip{}
		}

		if trip.Schedule != nil {
			if _, nextID, err := utils.ExtractAgencyIDAndCodeID(trip.Schedule.NextTripId); err == nil {
				rb.presentTrips[nextID] = models.Trip{}
			}
			if _, prevID, err := utils.ExtractAgencyIDAndCodeID(trip.Schedule.PreviousTripId); err == nil {
				rb.presentTrips[prevID] = models.Trip{}
			}
		}
	}
}

func (rb *referenceBuilder) buildStopList(stops []gtfsdb.Stop) {
	rb.stopList = make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIds, err := rb.api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStop(rb.ctx, stop.ID)
		if err != nil {
			continue
		}

		routeIdsString := rb.processRouteIds(routeIds)
		rb.stopList = append(rb.stopList, rb.createStop(stop, routeIdsString))
	}
}

func (rb *referenceBuilder) processRouteIds(routeIds []interface{}) []string {
	routeIdsString := make([]string, len(routeIds))
	for i, id := range routeIds {
		routeId := id.(string)
		rb.presentRoutes[routeId] = models.Route{}
		routeIdsString[i] = routeId
	}
	return routeIdsString
}

func (rb *referenceBuilder) createStop(stop gtfsdb.Stop, routeIds []string) models.Stop {
	direction := models.UnknownValue
	if stop.Direction.Valid && stop.Direction.String != "" {
		direction = stop.Direction.String
	}

	return models.Stop{
		Code:               utils.NullStringOrEmpty(stop.Code),
		Direction:          direction,
		ID:                 stop.ID,
		Lat:                stop.Lat,
		Lon:                stop.Lon,
		LocationType:       0,
		Name:               utils.NullStringOrEmpty(stop.Name),
		Parent:             "",
		RouteIDs:           routeIds,
		StaticRouteIDs:     routeIds,
		WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
	}
}

func (rb *referenceBuilder) enrichTripsData(allTrips []gtfsdb.Trip) {
	for _, trip := range allTrips {
		if _, exists := rb.presentTrips[trip.ID]; exists {
			rb.presentTrips[trip.ID] = rb.createTrip(trip)
			rb.presentRoutes[trip.RouteID] = models.Route{}
		}
	}
}

func (rb *referenceBuilder) createTrip(trip gtfsdb.Trip) models.Trip {
	return models.Trip{
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
}

func (rb *referenceBuilder) collectAgenciesAndRoutes(allRoutes []gtfsdb.Route) error {
	rb.presentAgencies = make(map[string]models.AgencyReference)

	for _, route := range allRoutes {
		if _, exists := rb.presentRoutes[route.ID]; !exists {
			continue
		}

		rb.presentRoutes[route.ID] = rb.createRoute(route)
		if err := rb.addAgency(route.AgencyID); err != nil {
			return err
		}
	}
	return nil
}

func (rb *referenceBuilder) createRoute(route gtfsdb.Route) models.Route {
	return models.NewRoute(
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
}

func (rb *referenceBuilder) addAgency(agencyID string) error {
	agency, err := rb.api.GtfsManager.GtfsDB.Queries.GetAgency(rb.ctx, agencyID)
	if err != nil {
		return err
	}

	rb.presentAgencies[agency.ID] = models.NewAgencyReference(
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
	)
	return nil
}

func (rb *referenceBuilder) buildTripReferences() error {
	rb.tripsRefList = make([]interface{}, 0, len(rb.presentTrips))

	for _, trip := range rb.presentTrips {
		tripDetails, err := rb.api.GtfsManager.GtfsDB.Queries.GetTrip(rb.ctx, trip.ID)
		if err != nil {
			continue
		}

		currentAgency := rb.presentRoutes[tripDetails.RouteID].AgencyID
		rb.tripsRefList = append(rb.tripsRefList, rb.createTripReference(tripDetails, currentAgency, trip))
	}
	return nil
}

func (rb *referenceBuilder) createTripReference(tripDetails gtfsdb.Trip, currentAgency string, trip models.Trip) models.Trip {
	return models.Trip{
		ID:            utils.FormCombinedID(currentAgency, trip.ID),
		RouteID:       utils.FormCombinedID(currentAgency, tripDetails.RouteID),
		ServiceID:     utils.FormCombinedID(currentAgency, trip.ServiceID),
		TripHeadsign:  tripDetails.TripHeadsign.String,
		TripShortName: tripDetails.TripShortName.String,
		DirectionID:   tripDetails.DirectionID.Int64,
		BlockID:       utils.FormCombinedID(currentAgency, trip.BlockID),
		ShapeID:       utils.FormCombinedID(currentAgency, tripDetails.ShapeID.String),
		PeakOffPeak:   0,
		TimeZone:      "",
	}
}

func (rb *referenceBuilder) toReferencesModel() models.ReferencesModel {
	return models.ReferencesModel{
		Agencies:   rb.getAgenciesList(),
		Routes:     rb.getRoutesList(),
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      rb.stopList,
		Trips:      rb.tripsRefList,
	}
}

func (rb *referenceBuilder) getAgenciesList() []models.AgencyReference {
	agencies := make([]models.AgencyReference, 0, len(rb.presentAgencies))
	for _, agency := range rb.presentAgencies {
		agencies = append(agencies, agency)
	}
	return agencies
}

func (rb *referenceBuilder) getRoutesList() []interface{} {
	routes := make([]interface{}, 0, len(rb.presentRoutes))
	for _, route := range rb.presentRoutes {
		if route.ID != "" {
			routes = append(routes, route)
		}
	}
	return routes
}
