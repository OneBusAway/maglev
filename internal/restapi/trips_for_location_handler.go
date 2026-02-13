package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

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

	visibleTripIDs := make([]string, 0, len(activeTrips))
	for _, vehicle := range activeTrips {
		if ctx.Err() != nil {
			return
		}

		if vehicle.Position == nil {
			continue
		}
		vLat, vLon := float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude)
		if vLat >= bbox.minLat && vLat <= bbox.maxLat && vLon >= bbox.minLon && vLon <= bbox.maxLon {
			visibleTripIDs = append(visibleTripIDs, vehicle.Trip.ID.ID)
		}
	}

	var trips []gtfsdb.Trip
	if len(visibleTripIDs) > 0 {
		trips, err = api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, visibleTripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	routeIDs := make([]string, 0, len(trips))
	tripRouteMap := make(map[string]string)
	for _, trip := range trips {
		routeIDs = append(routeIDs, trip.RouteID)
		tripRouteMap[trip.ID] = trip.RouteID
	}

	var routes []gtfsdb.Route
	if len(routeIDs) > 0 {
		routes, err = api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	tripAgencyMap := make(map[string]string)
	routeAgencyMap := make(map[string]string)

	for _, route := range routes {
		routeAgencyMap[route.ID] = route.AgencyID
	}
	for tripID, routeID := range tripRouteMap {
		if agencyID, ok := routeAgencyMap[routeID]; ok {
			tripAgencyMap[tripID] = agencyID
		}
	}

	// Build entries from pre-fetched trip data
	result := api.buildTripsForLocationEntries(ctx, trips, tripAgencyMap, includeSchedule, currentLocation, todayMidnight, serviceDate, w, r)
	if result == nil {
		return
	}

	if ctx.Err() != nil {
		return
	}

	references := api.BuildReference(w, r, ctx, ReferenceParams{
		IncludeTrip: includeTrip,
		Stops:       stops,
		Trips:       result,
	})
	response := models.NewListResponseWithRange(result, references, checkIfOutOfBounds(api, lat, lon, latSpan, lonSpan, 0), api.Clock, false)
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

	agencies := api.GtfsManager.GetAgencies()
	if len(agencies) == 0 {
		err := errors.New("no agencies configured in GTFS manager")
		api.serverErrorResponse(w, r, err)
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, err
	}
	currentAgency := agencies[0]
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

// buildTripsForLocationEntries builds trip entries from pre-fetched batch data.
func (api *RestAPI) buildTripsForLocationEntries(
	ctx context.Context,
	trips []gtfsdb.Trip,
	tripAgencyMap map[string]string,
	includeSchedule bool,
	currentLocation *time.Location,
	todayMidnight time.Time,
	serviceDate time.Time,
	w http.ResponseWriter,
	r *http.Request,
) []models.TripsForLocationListEntry {
	if len(trips) == 0 {
		return []models.TripsForLocationListEntry{}
	}

	tripsMap := make(map[string]gtfsdb.Trip)
	var shapeIDs []string
	uniqueBlockIDs := make(map[string]struct{})
	var validVehicleTrips []string

	for _, trip := range trips {
		// Ensure we only process trips that have a valid agency mapping
		if _, ok := tripAgencyMap[trip.ID]; !ok {
			continue
		}
		validVehicleTrips = append(validVehicleTrips, trip.ID)
		tripsMap[trip.ID] = trip
		if trip.ShapeID.Valid {
			shapeIDs = append(shapeIDs, trip.ShapeID.String)
		}
		if trip.BlockID.Valid {
			uniqueBlockIDs[trip.BlockID.String] = struct{}{}
		}
	}

	shapesMap := make(map[string][]gtfs.ShapePoint)
	if len(shapeIDs) > 0 {
		shapes, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByIDs(ctx, shapeIDs)
		if err == nil {
			for _, sp := range shapes {
				sid := sp.ShapeID
				shapesMap[sid] = append(shapesMap[sid], gtfs.ShapePoint{
					Latitude:  sp.Lat,
					Longitude: sp.Lon,
				})
			}
		} else {
			api.Logger.Warn("failed to bulk fetch shapes", "error", err)
		}
	}

	stopTimesMap := make(map[string][]gtfsdb.StopTime)
	blockTripsMap := make(map[string][]gtfsdb.Trip)
	var allStopIDs []string

	if includeSchedule {
		stopTimesRaw, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, validVehicleTrips)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return nil
		}
		for _, st := range stopTimesRaw {
			stopTimesMap[st.TripID] = append(stopTimesMap[st.TripID], st)
			allStopIDs = append(allStopIDs, st.StopID)
		}

		if len(uniqueBlockIDs) > 0 {
			var blockIDs []string
			for bid := range uniqueBlockIDs {
				blockIDs = append(blockIDs, bid)
			}

			dateStr := serviceDate.Format("20060102")
			activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, dateStr)
			if err != nil {
				activeServiceIDs = []string{}
				api.Logger.Warn("failed to fetch active service IDs for block logic", "error", err)
			}

			blockIDsNull := make([]sql.NullString, len(blockIDs))
			for i, id := range blockIDs {
				blockIDsNull[i] = sql.NullString{String: id, Valid: true}
			}

			params := gtfsdb.GetTripsByBlockIDsParams{
				BlockIds:   blockIDsNull,
				ServiceIds: activeServiceIDs,
			}

			blockTripsRaw, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDs(ctx, params)
			if err == nil {
				for _, bt := range blockTripsRaw {
					if bt.BlockID.Valid {
						bid := bt.BlockID.String
						blockTripsMap[bid] = append(blockTripsMap[bid], bt)
					}
				}
			} else {
				api.Logger.Warn("failed to bulk fetch block trips", "error", err)
			}
		}
	}

	stopCoords := make(map[string]struct{ lat, lon float64 })
	if len(allStopIDs) > 0 {
		stopsRaw, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, allStopIDs)
		if err == nil {
			for _, s := range stopsRaw {
				stopCoords[s.ID] = struct{ lat, lon float64 }{lat: s.Lat, lon: s.Lon}
			}
		} else {
			api.Logger.Warn("failed to bulk fetch stops", "error", err, "stop_count", len(allStopIDs))
		}
	}

	var result []models.TripsForLocationListEntry

	for _, tripID := range validVehicleTrips {
		if ctx.Err() != nil {
			return result
		}

		agencyID := tripAgencyMap[tripID]
		tripData, tripFound := tripsMap[tripID]
		if !tripFound {
			continue
		}

		var schedule *models.TripsSchedule
		if includeSchedule {
			var shapePoints []gtfs.ShapePoint
			if tripData.ShapeID.Valid {
				shapePoints = shapesMap[tripData.ShapeID.String]
			}

			var blockTrips []gtfsdb.Trip
			if tripData.BlockID.Valid {
				blockTrips = blockTripsMap[tripData.BlockID.String]
			}

			schedule = api.buildScheduleFromMemory(
				tripData,
				agencyID,
				currentLocation,
				stopTimesMap[tripID],
				shapePoints,
				stopCoords,
				blockTrips,
			)
		}
		entry := models.TripsForLocationListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(ctx, tripID),
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

	return api.calculateBatchStopDistances(stopTimes, shapePoints, stopCoords, agencyID)

}

type ReferenceParams struct {
	IncludeTrip bool
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

	rb.enrichTripsData()

	if err := rb.collectAgenciesAndRoutes(); err != nil {
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
		if rb.ctx.Err() != nil {
			return
		}

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

func (rb *referenceBuilder) enrichTripsData() {
	var tripIDs []string
	for id := range rb.presentTrips {
		tripIDs = append(tripIDs, id)
	}

	if len(tripIDs) == 0 {
		return
	}

	trips, err := rb.api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(rb.ctx, tripIDs)
	if err != nil {
		logging.LogError(rb.api.Logger, "failed to batch fetch trips for references", err)
		return
	}

	for _, trip := range trips {
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

func (rb *referenceBuilder) collectAgenciesAndRoutes() error {
	rb.presentAgencies = make(map[string]models.AgencyReference)

	var routeIDs []string
	for id := range rb.presentRoutes {
		routeIDs = append(routeIDs, id)
	}

	if len(routeIDs) == 0 {
		return nil
	}

	routes, err := rb.api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(rb.ctx, routeIDs)
	if err != nil {
		return err
	}

	for _, route := range routes {
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
		if rb.ctx.Err() != nil {
			return rb.ctx.Err()
		}

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

// buildScheduleFromMemory constructs a TripsSchedule from pre-fetched stop times, shape points, and block trips.
func (api *RestAPI) buildScheduleFromMemory(
	trip gtfsdb.Trip,
	agencyID string,
	currentLocation *time.Location,
	stopTimes []gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
	stopCoords map[string]struct{ lat, lon float64 },
	blockTrips []gtfsdb.Trip,
) *models.TripsSchedule {

	// Calculate Next/Prev using in-memory block trips
	nextTripID, previousTripID := api.calculateNextPrevFromMemory(trip, blockTrips, agencyID)

	// Calculate Distances using in-memory coords
	stopTimesList := api.calculateBatchStopDistances(stopTimes, shapePoints, stopCoords, agencyID)

	return &models.TripsSchedule{
		Frequency:      nil,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}
}

// calculateNextPrevFromMemory determines the next and previous trip IDs within a block.
func (api *RestAPI) calculateNextPrevFromMemory(currentTrip gtfsdb.Trip, blockTrips []gtfsdb.Trip, agencyID string) (string, string) {
	if len(blockTrips) == 0 {
		return "", ""
	}

	// Filter blockTrips to only include those that share the exact ServiceID of the current trip.
	// This ensures we don't mix trips from different service days (e.g. Weekday vs Weekend).
	var relevantTrips []gtfsdb.Trip
	for _, t := range blockTrips {
		if t.ServiceID == currentTrip.ServiceID {
			relevantTrips = append(relevantTrips, t)
		}
	}

	if len(relevantTrips) == 0 {
		return "", ""
	}

	// Find index of current trip in the ordered list
	currentIndex := -1
	for i, t := range relevantTrips {
		if t.ID == currentTrip.ID {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return "", ""
	}

	var next, prev string

	// BlockTrips are already ordered by departure_time via the SQL query (GetTripsByBlockIDs)
	if currentIndex < len(relevantTrips)-1 {
		next = utils.FormCombinedID(agencyID, relevantTrips[currentIndex+1].ID)
	}
	if currentIndex > 0 {
		prev = utils.FormCombinedID(agencyID, relevantTrips[currentIndex-1].ID)
	}

	return next, prev
}
