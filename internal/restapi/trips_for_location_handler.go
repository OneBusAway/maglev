package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForLocationHandler returns active trips near a geographic location, specified by
// lat/lon coordinates with latSpan/lonSpan bounds, including real-time status and schedule data.
func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	parsedReq, fieldErrors, err := api.parseAndValidateRequest(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Uncapped: this stop set only narrows the candidate trips, and a cap here
	// silently drops trips the spec says should all be returned.
	stopsInBounds := api.GtfsManager.GetStopsInBounds(ctx, parsedReq.LocationParams, 0, true)
	stopIDs := extractStopIDs(stopsInBounds)
	candidateTripIDs, err := api.candidateTripIDsForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	activeTrips := api.getActiveTrips(candidateTripIDs, api.GtfsManager.GetRealTimeVehicles())

	bounds := internalgtfs.BoundsFromParams(parsedReq.LocationParams, true)
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
		if vLat >= bounds.MinLat && vLat <= bounds.MaxLat && vLon >= bounds.MinLon && vLon <= bounds.MaxLon {
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
	result, situations := api.buildTripsForLocationEntries(ctx, trips, tripAgencyMap, routeAgencyMap, parsedReq, w, r)
	if result == nil {
		return
	}

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	references := *models.NewEmptyReferences()

	includeReferences := ShouldIncludeReferences(r)

	if includeReferences {
		referencedStops, stopIDsByBareID, stopsErr := api.stopsReferencedByEntries(ctx, result)
		if stopsErr != nil {
			api.serverErrorResponse(w, r, stopsErr)
			return
		}

		references = api.BuildReference(w, r, ctx, ReferenceParams{
			IncludeTrip:     parsedReq.IncludeTrip,
			Stops:           referencedStops,
			StopIDsByBareID: stopIDsByBareID,
			Trips:           result,
			Situations:      situations,
		})
	}

	// The search clamped its bounds, so outOfRange has to be reported against
	// the clamped bounds too.
	outOfRange := api.GtfsManager.CheckIfOutOfBounds(parsedReq.LocationParams, true)
	response := models.NewListResponseWithRange(result, references, outOfRange, api.Clock, false)
	api.sendResponse(w, r, response)
}

// tripsForLocationRequest holds the parsed and validated query parameters for
// the trips-for-location endpoint.
type tripsForLocationRequest struct {
	LocationParams  *internalgtfs.LocationParams
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	CurrentTime     time.Time
	AgencyLocations map[string]*time.Location
}

func (api *RestAPI) parseAndValidateRequest(r *http.Request) (*tripsForLocationRequest, map[string][]string, error) {
	loc, fieldErrors := api.parseLocationParams(r, nil)

	queryParams := r.URL.Query()

	includeTrip := parseIncludeTrip(queryParams)
	includeSchedule, _ := strconv.ParseBool(queryParams.Get("includeSchedule"))
	// Intentionally defaulting includeStatus to false to align with includeSchedule
	// behavior for this endpoint, even though trips-for-route defaults to true.
	includeStatus, _ := strconv.ParseBool(queryParams.Get("includeStatus"))

	agencies, agenciesErr := api.GtfsManager.GetAgencies(r.Context())

	if agenciesErr != nil {
		return nil, nil, agenciesErr
	}

	if len(agencies) == 0 {
		return nil, nil, errors.New("no agencies configured in GTFS manager")
	}

	agencyLocations := make(map[string]*time.Location, len(agencies))
	for _, agency := range agencies {
		location, locationErr := loadAgencyLocation(agency.ID, agency.Timezone)
		if locationErr != nil {
			return nil, nil, locationErr
		}
		agencyLocations[agency.ID] = location
	}
	currentLocation := agencyLocations[agencies[0].ID]

	currentTime, timeFieldErrors := api.resolveCurrentTime(queryParams.Get("time"), currentLocation)
	fieldErrors = mergeFieldErrors(fieldErrors, timeFieldErrors)

	if len(fieldErrors) > 0 {
		return nil, fieldErrors, nil
	}

	parsedReq := &tripsForLocationRequest{
		LocationParams:  loc,
		IncludeTrip:     includeTrip,
		IncludeSchedule: includeSchedule,
		IncludeStatus:   includeStatus,
		CurrentTime:     currentTime,
		AgencyLocations: agencyLocations,
	}
	return parsedReq, nil, nil
}

// parseIncludeTrip parses the includeTrip query parameter, defaulting to true when omitted
// and to false when present but not a valid boolean.
func parseIncludeTrip(queryParams url.Values) bool {
	if !queryParams.Has("includeTrip") {
		return true
	}
	includeTrip, _ := strconv.ParseBool(queryParams.Get("includeTrip"))
	return includeTrip
}

// resolveCurrentTime resolves the query time: the explicit time parameter if supplied,
// otherwise the current server clock.
func (api *RestAPI) resolveCurrentTime(timeParam string, currentLocation *time.Location) (time.Time, map[string][]string) {
	if timeParam == "" {
		return api.Clock.Now().In(currentLocation), nil
	}
	_, currentTime, timeFieldErrors, _ := utils.ParseTimeParameter(timeParam, currentLocation, api.Clock)
	return currentTime, timeFieldErrors
}

// mergeFieldErrors appends src's entries onto dst, allocating dst if necessary.
func mergeFieldErrors(dst, src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string][]string)
	}
	for k, v := range src {
		dst[k] = append(dst[k], v...)
	}
	return dst
}

// stopsReferencedByEntries fetches the stops the response actually refers to:
// those on each entry's schedule, plus the closest and next stops on its status.
// The in-bounds stop set is deliberately not included — it is a candidate-trip
// selection detail, and stops on it that no returned trip serves have nothing in
// the response pointing at them.
func (api *RestAPI) stopsReferencedByEntries(ctx context.Context, entries []models.TripsForLocationListEntry) ([]gtfsdb.Stop, map[string]string, error) {
	stopIDsByBareID := make(map[string]string)

	for _, entry := range entries {
		collectStopIDsFromSchedule(entry.Schedule, stopIDsByBareID)
		if entry.Status == nil {
			continue
		}
		for _, combinedID := range []string{entry.Status.ClosestStop, entry.Status.NextStop} {
			_, bareID, err := utils.ExtractAgencyIDAndCodeID(combinedID)
			if err != nil {
				continue
			}
			if _, exists := stopIDsByBareID[bareID]; !exists {
				stopIDsByBareID[bareID] = combinedID
			}
		}
	}

	if len(stopIDsByBareID) == 0 {
		return nil, nil, nil
	}

	bareIDs := make([]string, 0, len(stopIDsByBareID))
	for bareID := range stopIDsByBareID {
		bareIDs = append(bareIDs, bareID)
	}

	stops, err := queryInBatches(ctx, bareIDs, api.GtfsManager.GtfsDB.Queries.GetStopsByIDs)
	return stops, stopIDsByBareID, err
}

// candidateTripIDsForStops returns the IDs of the trips serving any of these
// stops. IDs may repeat across batches; the caller sets them.
func (api *RestAPI) candidateTripIDsForStops(ctx context.Context, stopIDs []string) ([]string, error) {
	return queryInBatches(ctx, stopIDs, api.GtfsManager.GtfsDB.Queries.GetTripIDsForStops)
}

func extractStopIDs(stops []gtfsdb.Stop) []string {
	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.ID
	}
	return stopIDs
}

func (api *RestAPI) getActiveTrips(candidateTripIDs []string, realTimeVehicles []gtfs.Vehicle) map[string]gtfs.Vehicle {
	trips := make(map[string]bool, len(candidateTripIDs))
	for _, tripID := range candidateTripIDs {
		trips[tripID] = true
	}
	activeTrips := make(map[string]gtfs.Vehicle)
	for _, vehicle := range realTimeVehicles {
		if vehicle.Trip != nil && trips[vehicle.Trip.ID.ID] {
			activeTrips[vehicle.Trip.ID.ID] = vehicle
		}
	}
	return activeTrips
}

// buildTripsForLocationEntries builds trip entries from pre-fetched batch data,
// returning the entries alongside the situations they reference.
// It returns nil entries only when an error response has already been sent to
// the client.
func (api *RestAPI) buildTripsForLocationEntries(
	ctx context.Context,
	trips []gtfsdb.Trip,
	tripAgencyMap map[string]string,
	routeAgencyMap map[string]string,
	request *tripsForLocationRequest,
	w http.ResponseWriter,
	r *http.Request,
) ([]models.TripsForLocationListEntry, []situationRef) {
	if len(trips) == 0 {
		return []models.TripsForLocationListEntry{}, nil
	}

	tripsMap := make(map[string]gtfsdb.Trip)
	var shapeIDs []string
	blockIDsByAgency := make(map[string]map[string]struct{})
	agencyIDs := make(map[string]struct{})
	var validVehicleTrips []string

	for _, trip := range trips {
		// Ensure we only process trips that have a valid agency mapping
		agencyID, ok := tripAgencyMap[trip.ID]
		if !ok {
			continue
		}
		validVehicleTrips = append(validVehicleTrips, trip.ID)
		tripsMap[trip.ID] = trip
		if trip.ShapeID.Valid {
			shapeIDs = append(shapeIDs, trip.ShapeID.String)
		}

		// A trip whose agency has no resolvable timezone is skipped later when
		// entries are built, so its block and service-day data is never needed.
		if _, hasLocation := request.AgencyLocations[agencyID]; !hasLocation {
			continue
		}
		agencyIDs[agencyID] = struct{}{}
		if trip.BlockID.Valid {
			agencyBlockIDs := blockIDsByAgency[agencyID]
			if agencyBlockIDs == nil {
				agencyBlockIDs = make(map[string]struct{})
				blockIDsByAgency[agencyID] = agencyBlockIDs
			}
			agencyBlockIDs[trip.BlockID.String] = struct{}{}
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

	// Every entry needs its agency's resolver regardless of includeSchedule, and
	// the block lookup below needs that same agency's query-day service IDs —
	// fetched once per agency here rather than the block lookup querying it
	// again itself.
	services := make(map[string]serviceIDsByDay, len(agencyIDs))
	serviceDatesByAgency := make(map[string]*serviceDateResolver, len(agencyIDs))
	for agencyID := range agencyIDs {
		agencyLocation := request.AgencyLocations[agencyID]
		queryDayMidnight := serviceDateMidnight(request.CurrentTime, agencyLocation)
		days := api.serviceIDsForDays(ctx, queryDayMidnight)
		services[agencyID] = days
		serviceDatesByAgency[agencyID] = newServiceDateResolverFor(
			queryDayMidnight, request.CurrentTime.In(agencyLocation), days)
	}

	stopTimesMap := make(map[string][]gtfsdb.StopTime)
	blockTripsMap := make(map[blockTripsKey][]gtfsdb.GetTripsByBlockIDsRow)
	var allStopIDs []string

	if request.IncludeSchedule {
		stopTimesRaw, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, validVehicleTrips)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return nil, nil
		}
		for _, st := range stopTimesRaw {
			stopTimesMap[st.TripID] = append(stopTimesMap[st.TripID], st)
			allStopIDs = append(allStopIDs, st.StopID)
		}

		for agencyID, agencyBlockIDs := range blockIDsByAgency {
			blockIDsNull := make([]sql.NullString, 0, len(agencyBlockIDs))
			for id := range agencyBlockIDs {
				blockIDsNull = append(blockIDsNull, nulls.String(id))
			}

			params := gtfsdb.GetTripsByBlockIDsParams{
				BlockIds:   blockIDsNull,
				ServiceIds: services[agencyID].QueryDay,
			}

			blockTripsRaw, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDs(ctx, params)
			if err == nil {
				missingRouteIDs := make([]string, 0)
				for _, bt := range blockTripsRaw {
					if _, found := routeAgencyMap[bt.RouteID]; !found {
						missingRouteIDs = append(missingRouteIDs, bt.RouteID)
					}
				}
				if len(missingRouteIDs) > 0 {
					routes, routeErr := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, missingRouteIDs)
					if routeErr != nil {
						api.Logger.Warn("failed to fetch block trip routes", "agency_id", agencyID, "error", routeErr)
						continue
					}
					for _, route := range routes {
						routeAgencyMap[route.ID] = route.AgencyID
					}
				}
				for _, bt := range blockTripsRaw {
					if bt.BlockID.Valid && routeAgencyMap[bt.RouteID] == agencyID {
						key := blockTripsKey{agencyID: agencyID, blockID: bt.BlockID.String}
						blockTripsMap[key] = append(blockTripsMap[key], bt)
					}
				}
			} else {
				api.Logger.Warn("failed to bulk fetch block trips", "agency_id", agencyID, "error", err)
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
	situations := newSituationCollector()

	for _, tripID := range validVehicleTrips {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return nil, nil
		}

		agencyID := tripAgencyMap[tripID]
		tripData, tripFound := tripsMap[tripID]
		if !tripFound {
			continue
		}
		agencyLocation, locationFound := request.AgencyLocations[agencyID]
		if !locationFound {
			api.Logger.Warn("missing timezone for trip agency", "trip_id", tripID, "agency_id", agencyID)
			continue
		}

		var schedule *models.TripsSchedule
		var status *models.TripStatus

		if request.IncludeSchedule {
			var shapePoints []gtfs.ShapePoint
			if tripData.ShapeID.Valid {
				shapePoints = shapesMap[tripData.ShapeID.String]
			}

			var blockTrips []gtfsdb.GetTripsByBlockIDsRow
			if tripData.BlockID.Valid {
				blockTrips = blockTripsMap[blockTripsKey{agencyID: agencyID, blockID: tripData.BlockID.String}]
			}

			schedule = api.buildScheduleFromMemory(
				tripData,
				agencyID,
				agencyLocation,
				stopTimesMap[tripID],
				shapePoints,
				stopCoords,
				blockTrips,
			)
		}

		serviceDate := serviceDatesByAgency[agencyID].Resolve(tripData)

		if request.IncludeStatus {
			var statusErr error
			status, _, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, serviceDate, request.CurrentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "tripID", tripID, "error", statusErr)
				status = nil
			}
		}

		// The trip's route and agency are already resolved here, so the alerts
		// are looked up directly rather than through GetSituationIDsForTrip,
		// which would re-query both per trip and discard the alerts we need
		// for the situation references.
		alerts := api.GtfsManager.GetAlertsByIDs(tripID, tripData.RouteID, agencyID)

		entry := models.TripsForLocationListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  serviceDate.UnixMilli(),
			SituationIds: situations.add(alerts, agencyID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}
	return result, situations.refs
}

type blockTripsKey struct {
	agencyID string
	blockID  string
}

// serviceDateMidnight returns the start of the service day in an agency's timezone.
func serviceDateMidnight(currentTime time.Time, agencyLocation *time.Location) time.Time {
	_, midnight := utils.ServiceDateMidnight(nil, currentTime.In(agencyLocation))
	return midnight
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
	return &models.TripsSchedule{
		Frequency:      nil,
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
	// StopIDsByBareID maps each stop's bare ID to the combined ID the entries
	// referred to it by, so a reference is published under the ID pointing at it.
	StopIDsByBareID map[string]string
	Trips           []models.TripsForLocationListEntry
	Situations      []situationRef
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
	situations      []situationRef
}

func (rb *referenceBuilder) build(params ReferenceParams) error {
	rb.situations = params.Situations
	rb.collectTripIDs(params.Trips)
	rb.buildStopList(params.Stops, params.StopIDsByBareID)

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

// buildStopList emits the stop references and registers the routes serving them.
// A stop referred to by more than one agency still gets a single reference — the
// first ID seen wins, and the other stays dangling.
func (rb *referenceBuilder) buildStopList(stops []gtfsdb.Stop, stopIDsByBareID map[string]string) {
	stopList, routeIDsByStop := rb.api.stopReferences(rb.ctx, stops, stopIDsByBareID)
	rb.stopList = stopList

	// Register the raw route ID (e.g. "100479") rather than the combined one, so
	// that collectAgenciesAndRoutes can fetch full route details via
	// GetRoutesByIDs, which queries WHERE routes.id IN (?) using raw IDs.
	for _, combinedRouteIDs := range routeIDsByStop {
		for _, combinedID := range combinedRouteIDs {
			if rawID, err := utils.ExtractCodeID(combinedID); err == nil {
				rb.presentRoutes[rawID] = models.Route{}
			}
		}
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
		rb.presentAgencies[agency.ID] = models.AgencyReferenceFromDatabase(&agency)
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
	references.Situations = rb.getSituationsList()

	return *references
}

func (rb *referenceBuilder) getSituationsList() []models.Situation {
	return rb.api.situationReferences(rb.situations)
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

	return &models.TripsSchedule{
		Frequency:      nil,
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
