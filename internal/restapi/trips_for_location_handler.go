package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	gtfsInternal "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForLocationHandler returns active trips near a geographic location, specified by
// lat/lon coordinates with latSpan/lonSpan bounds, including real-time status and schedule data.
func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, fieldErrors, err := api.parseAndValidateRequest(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Intentionally defaulting includeStatus to false to align with includeSchedule
	// behavior for this endpoint, even though trips-for-route defaults to true.
	includeStatus := r.URL.Query().Get("includeStatus") == "true"
	// Note: re-deriving currentTime here rather than returning it from parseAndValidateRequest(line: 150)
	currentTime := api.Clock.Now().In(currentLocation)

	stops := api.GtfsManager.GetStopsInBounds(ctx, lat, lon, -1, latSpan, lonSpan, 100)
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
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		if vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
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
	result := api.buildTripsForLocationEntries(ctx, trips, tripAgencyMap, includeSchedule, includeStatus, currentLocation, currentTime, todayMidnight, serviceDate, w, r)
	if result == nil {
		return
	}

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
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

func (api *RestAPI) parseAndValidateRequest(r *http.Request) (
	lat, lon, latSpan, lonSpan float64,
	includeTrip, includeSchedule bool,
	currentLocation *time.Location,
	todayMidnight time.Time,
	serviceDate time.Time,
	fieldErrors map[string][]string,
	serverErr error,
) {
	var loc *LocationParams
	loc, fieldErrors = api.parseLocationParams(r, nil)

	if loc != nil {
		lat = loc.Lat
		lon = loc.Lon
		latSpan = loc.LatSpan
		lonSpan = loc.LonSpan
	}

	queryParams := r.URL.Query()

	includeTrip = queryParams.Get("includeTrip") == "true"
	includeSchedule = queryParams.Get("includeSchedule") == "true"

	agencies, agenciesErr := api.GtfsManager.GetAgencies(r.Context())
	if agenciesErr != nil || len(agencies) == 0 {
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, nil, errors.New("no agencies configured in GTFS manager")
	}

	currentAgency := agencies[0]
	currentLocation, serverErr = loadAgencyLocation(currentAgency.ID, currentAgency.Timezone)
	if serverErr != nil {
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, nil, serverErr
	}

	timeParam := queryParams.Get("time")
	currentTime := api.Clock.Now().In(currentLocation)
	todayMidnight = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)

	var timeFieldErrors map[string][]string
	_, serviceDate, timeFieldErrors, _ = utils.ParseTimeParameter(timeParam, currentLocation)
	if len(timeFieldErrors) > 0 {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		for k, v := range timeFieldErrors {
			fieldErrors[k] = append(fieldErrors[k], v...)
		}
	}

	ctx := r.Context()
	if ctx.Err() != nil {
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, nil, ctx.Err()
	}

	if len(fieldErrors) > 0 {
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, fieldErrors, nil
	}

	return lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, nil, nil
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
	includeStatus bool,
	currentLocation *time.Location,
	currentTime time.Time,
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
	blockTripsMap := make(map[string][]gtfsdb.GetTripsByBlockIDsRow)
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
		var status *models.TripStatus

		if includeSchedule {
			var shapePoints []gtfs.ShapePoint
			if tripData.ShapeID.Valid {
				shapePoints = shapesMap[tripData.ShapeID.String]
			}

			var blockTrips []gtfsdb.GetTripsByBlockIDsRow
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

		if includeStatus {
			var statusErr error
			status, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, todayMidnight, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "tripID", tripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForLocationListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(ctx, tripID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}

	// Batch-fetch frequencies for all trips in the result to avoid N+1 queries
	if len(validVehicleTrips) > 0 {
		allFreqs, freqErr := api.GtfsManager.GetFrequenciesForTrips(ctx, validVehicleTrips)
		if freqErr != nil {
			api.Logger.Warn("failed to batch fetch frequencies", "error", freqErr)
		}
		if len(allFreqs) > 0 {
			freqsByTrip := gtfsInternal.GroupFrequenciesByTrip(allFreqs)
			for i := range result {
				_, rawTripID, err := utils.ExtractAgencyIDAndCodeID(result[i].TripId)
				if err != nil {
					continue
				}
				if freqs, ok := freqsByTrip[rawTripID]; ok && len(freqs) > 0 {
					// For trips-for-location, Frequency is *int64 (headway in seconds)
					headway := freqs[0].HeadwaySecs
					result[i].Frequency = &headway
				}
			}
		}
	}

	return result
}

func (api *RestAPI) buildScheduleForTrip(
	ctx context.Context,
	tripID, agencyID string, serviceDate time.Time,
	currentLocation *time.Location,
) (*models.TripsSchedule, error) {
	shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	var shapePoints []gtfs.ShapePoint
	if len(shapeRows) > 1 {
		shapePoints = shapeRowsToPoints(shapeRows)
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	nextTripID, previousTripID, stopTimes, err := api.GetNextAndPreviousTripIDs(ctx, &trip, agencyID, serviceDate)
	if err != nil {
		return nil, err
	}

	stopTimesList := buildStopTimesList(api, ctx, stopTimes, shapePoints, agencyID)

	// Look up frequency data for this trip's schedule
	var scheduleFreq *models.Frequency
	freqs, freqErr := api.GtfsManager.GetFrequenciesForTrip(ctx, tripID)
	if freqErr == nil && len(freqs) > 0 {
		f := models.NewFrequencyFromDB(freqs[0], serviceDate)
		scheduleFreq = &f
	}

	return &models.TripsSchedule{
		Frequency:      scheduleFreq,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}, nil
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
	tripsRefList    []models.Trip
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
		if err == nil {
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

		if trip.Status != nil && trip.Status.ActiveTripID != "" {
			if _, activeID, err := utils.ExtractAgencyIDAndCodeID(trip.Status.ActiveTripID); err == nil {
				rb.presentTrips[activeID] = models.Trip{}
			}
		}

	}
}

func (rb *referenceBuilder) buildStopList(stops []gtfsdb.Stop) {
	rb.stopList = make([]models.Stop, 0, len(stops))
	if len(stops) == 0 {
		return
	}

	stopIDs := make([]string, 0, len(stops))
	for _, stop := range stops {
		stopIDs = append(stopIDs, stop.ID)
	}

	routesForStops, err := rb.api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(rb.ctx, stopIDs)
	if err != nil {
		logging.LogError(rb.api.Logger, "failed to batch fetch routes for stops", err)
		return
	}

	// Build in-memory map: stopID → []combinedRouteID (e.g. "40_100479").
	// Also register the raw route ID (e.g. "100479") in presentRoutes so that
	// collectAgenciesAndRoutes can fetch full route details via GetRoutesByIDs,
	// which queries WHERE routes.id IN (?), using raw IDs — not combined ones.
	stopRouteMap := make(map[string][]string)
	for _, r := range routesForStops {
		combinedID, ok := r.RouteID.(string)
		if !ok {
			continue
		}
		stopRouteMap[r.StopID] = append(stopRouteMap[r.StopID], combinedID)
		if rawID, err := utils.ExtractCodeID(combinedID); err == nil {
			rb.presentRoutes[rawID] = models.Route{}
		}
	}

	for _, stop := range stops {
		if rb.ctx.Err() != nil {
			return
		}
		combinedRouteIDs := stopRouteMap[stop.ID]
		if len(combinedRouteIDs) == 0 {
			continue
		}
		rb.stopList = append(rb.stopList, rb.createStop(stop, combinedRouteIDs))
	}
}

func (rb *referenceBuilder) createStop(stop gtfsdb.Stop, routeIds []string) models.Stop {
	agencyID := ""
	if len(routeIds) > 0 {
		if id, err := utils.ExtractAgencyID(routeIds[0]); err == nil {
			agencyID = id
		}
	}

	direction := rb.api.DirectionCalculator.CalculateStopDirection(rb.ctx, stop.ID, stop.Direction)

	return models.Stop{
		Code:               utils.NullStringOrEmpty(stop.Code),
		Direction:          direction,
		ID:                 utils.FormCombinedID(agencyID, stop.ID),
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
		DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
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

	agencyIDSet := make(map[string]struct{})
	for _, route := range routes {
		rb.presentRoutes[route.ID] = rb.createRoute(route)
		agencyIDSet[route.AgencyID] = struct{}{}
	}

	uniqueAgencyIDs := make([]string, 0, len(agencyIDSet))
	for id := range agencyIDSet {
		uniqueAgencyIDs = append(uniqueAgencyIDs, id)
	}

	agencies, err := rb.api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(rb.ctx, uniqueAgencyIDs)
	if err != nil {
		return err
	}

	for _, agency := range agencies {
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
		route.TextColor.String)

}

func (rb *referenceBuilder) buildTripReferences() error {
	rb.tripsRefList = make([]models.Trip, 0, len(rb.presentTrips))

	for _, trip := range rb.presentTrips {
		if rb.ctx.Err() != nil {
			return rb.ctx.Err()
		}

		if trip.ID == "" {
			continue
		}

		route, ok := rb.presentRoutes[trip.RouteID]
		if !ok {
			continue
		}
		rb.tripsRefList = append(rb.tripsRefList, rb.createTripReference(trip, route.AgencyID))
	}
	return nil
}

func (rb *referenceBuilder) createTripReference(trip models.Trip, currentAgency string) models.Trip {
	return models.Trip{
		ID:            utils.FormCombinedID(currentAgency, trip.ID),
		RouteID:       utils.FormCombinedID(currentAgency, trip.RouteID),
		ServiceID:     utils.FormCombinedID(currentAgency, trip.ServiceID),
		TripHeadsign:  trip.TripHeadsign,
		TripShortName: trip.TripShortName,
		DirectionID:   trip.DirectionID,
		BlockID:       utils.FormCombinedID(currentAgency, trip.BlockID),
		ShapeID:       utils.FormCombinedID(currentAgency, trip.ShapeID),
		PeakOffPeak:   0,
		TimeZone:      "",
	}
}

func (rb *referenceBuilder) toReferencesModel() models.ReferencesModel {
	trips := rb.tripsRefList
	if trips == nil {
		trips = []models.Trip{}
	}
	stops := rb.stopList
	if stops == nil {
		stops = []models.Stop{}
	}

	references := models.NewEmptyReferences()
	references.Agencies = rb.getAgenciesList()
	references.Routes = rb.getRoutesList()
	references.Stops = stops
	references.Trips = trips

	return *references
}

func (rb *referenceBuilder) getAgenciesList() []models.AgencyReference {
	agencies := make([]models.AgencyReference, 0, len(rb.presentAgencies))
	for _, agency := range rb.presentAgencies {
		agencies = append(agencies, agency)
	}
	return agencies
}

func (rb *referenceBuilder) getRoutesList() []models.Route {
	routes := make([]models.Route, 0, len(rb.presentRoutes))
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
	blockTrips []gtfsdb.GetTripsByBlockIDsRow,
) *models.TripsSchedule {

	// Calculate Next/Prev using in-memory block trips
	nextTripID, previousTripID := api.calculateNextPrevFromMemory(trip, blockTrips, agencyID)

	// Calculate Distances using in-memory coords
	stopTimesList := api.calculateBatchStopDistances(stopTimes, shapePoints, stopCoords, agencyID)

	// Look up frequency data for this trip's schedule
	var scheduleFreq *models.Frequency
	freqs, freqErr := api.GtfsManager.GetFrequenciesForTrip(context.Background(), trip.ID)
	if freqErr == nil && len(freqs) > 0 {
		// Use midnight of current time for the service date
		now := api.Clock.Now()
		serviceDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, currentLocation)
		f := models.NewFrequencyFromDB(freqs[0], serviceDate)
		scheduleFreq = &f
	}

	return &models.TripsSchedule{
		Frequency:      scheduleFreq,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}
}

// calculateNextPrevFromMemory determines the next and previous trip IDs within a block.
func (api *RestAPI) calculateNextPrevFromMemory(currentTrip gtfsdb.Trip, blockTrips []gtfsdb.GetTripsByBlockIDsRow, agencyID string) (string, string) {
	if len(blockTrips) == 0 {
		return "", ""
	}

	// Filter blockTrips to only include those that share the exact ServiceID of the current trip.
	// This ensures we don't mix trips from different service days (e.g. Weekday vs Weekend).
	var relevantTrips []gtfsdb.GetTripsByBlockIDsRow
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
