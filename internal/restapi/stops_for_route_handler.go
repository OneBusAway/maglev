package restapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/gtfsdb"
	GTFS "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type stopsForRouteParams struct {
	IncludePolylines bool
	Time             *time.Time
}

func (api *RestAPI) parseStopsForRouteParams(r *http.Request) stopsForRouteParams {
	now := api.Clock.Now()
	params := stopsForRouteParams{
		IncludePolylines: true,
		Time:             &now,
	}

	if r.URL.Query().Get("includePolylines") == "false" {
		params.IncludePolylines = false
	}

	if timeParam := r.URL.Query().Get("time"); timeParam != "" {
		if t, err := time.Parse(time.RFC3339, timeParam); err == nil {
			params.Time = &t
		}
	}
	return params
}

func (api *RestAPI) stopsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return
	}
	id := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(id); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, routeID, err := utils.ExtractAgencyIDAndCodeID(id)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	params := api.parseStopsForRouteParams(r)

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	currentLocation, err := time.LoadLocation(currentAgency.Timezone)
	// Fallback to UTC on error
	if err != nil {
		slog.Warn("failed to load agency timezone, defaulting to UTC",
			slog.String("agencyID", agencyID),
			slog.String("timezone", currentAgency.Timezone),
			slog.String("error", err.Error()))
		currentLocation = time.UTC
	}

	timeParam := r.URL.Query().Get("time")
	formattedDate, _, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	_, err = api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// This prevents nil pointer panics and ensures thread-safety.
	adc := GTFS.NewAdvancedDirectionCalculator(api.GtfsManager.GtfsDB.Queries)

	// Get Stop IDs for the route to drive the bulk-loading caches
	stopIDs, err := api.GtfsManager.GtfsDB.Queries.GetStopIDsForRoute(ctx, routeID)
	if err == nil && len(stopIDs) > 0 {

		contextRows, err := api.GtfsManager.GtfsDB.Queries.GetStopsWithShapeContextByIDs(ctx, stopIDs)
		if err != nil {
			// Log error when bulk context load fails
			slog.Warn("bulk context cache load failed, falling back to per-stop queries",
				slog.String("routeID", routeID),
				slog.String("error", err.Error()))
		} else {
			contextCache := make(map[string][]gtfsdb.GetStopsWithShapeContextRow)
			shapeIDMap := make(map[string]bool)
			var uniqueShapeIDs []string

			for _, row := range contextRows {
				calcRow := gtfsdb.GetStopsWithShapeContextRow{
					ID:                row.StopID,
					ShapeID:           row.ShapeID,
					Lat:               row.Lat,
					Lon:               row.Lon,
					ShapeDistTraveled: row.ShapeDistTraveled,
				}
				contextCache[row.StopID] = append(contextCache[row.StopID], calcRow)

				if row.ShapeID.Valid && row.ShapeID.String != "" && !shapeIDMap[row.ShapeID.String] {
					shapeIDMap[row.ShapeID.String] = true
					uniqueShapeIDs = append(uniqueShapeIDs, row.ShapeID.String)
				}
			}

			// Fetch Shape Points in bulk to populate the local cache
			if len(uniqueShapeIDs) > 0 {
				shapePoints, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByIDs(ctx, uniqueShapeIDs)
				if err != nil {
					// Log error when bulk shape load fails
					slog.Warn("bulk shape cache load failed, falling back to per-stop queries",
						slog.String("routeID", routeID),
						slog.String("error", err.Error()))
				} else {
					shapeCache := make(map[string][]gtfsdb.GetShapePointsWithDistanceRow)
					for _, p := range shapePoints {
						shapeCache[p.ShapeID] = append(shapeCache[p.ShapeID], gtfsdb.GetShapePointsWithDistanceRow{
							Lat:               p.Lat,
							Lon:               p.Lon,
							ShapeDistTraveled: p.ShapeDistTraveled,
						})
					}

					// Inject caches into the LOCAL instance.
					adc.SetShapeCache(shapeCache)
					adc.SetContextCache(contextCache)
				}
			}
		}
	}

	result, stopsList, err := api.processRouteStops(ctx, agencyID, routeID, serviceIDs, params.IncludePolylines, adc)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	api.buildAndSendResponse(w, r, ctx, result, stopsList, currentAgency)
}
func (api *RestAPI) processRouteStops(ctx context.Context, agencyID string, routeID string, serviceIDs []string, includePolylines bool, adc *GTFS.AdvancedDirectionCalculator) (models.RouteEntry, []models.Stop, error) {
	allStops := make(map[string]bool)
	allPolylines := make([]models.Polyline, 0, 100)
	var stopGroupings []models.StopGrouping

	// Get trips for route that are active on the service date
	trips, err := api.GtfsManager.GtfsDB.Queries.GetTripsForRouteInActiveServiceIDs(ctx, gtfsdb.GetTripsForRouteInActiveServiceIDsParams{
		RouteID:    routeID,
		ServiceIds: serviceIDs,
	})

	if err != nil {
		return models.RouteEntry{}, nil, err
	}

	if len(trips) == 0 {
		// Fallback: get all trips for this route regardless of service date
		allTrips, err := api.GtfsManager.GtfsDB.Queries.GetAllTripsForRoute(ctx, routeID)
		if err != nil {
			return models.RouteEntry{}, nil, err
		}
		processTripGroups(ctx, api, agencyID, routeID, allTrips, &stopGroupings, allStops, &allPolylines)
	} else {
		// Process trips for the current service date
		processTripGroups(ctx, api, agencyID, routeID, trips, &stopGroupings, allStops, &allPolylines)
	}

	if !includePolylines {
		allPolylines = []models.Polyline{}
	}

	allStopsIds := formatStopIDs(agencyID, allStops)
	stopsList, err := buildStopsList(ctx, api, adc, agencyID, allStops)
	if err != nil {
		return models.RouteEntry{}, nil, err
	}

	result := models.RouteEntry{
		Polylines:     allPolylines,
		RouteID:       utils.FormCombinedID(agencyID, routeID),
		StopGroupings: stopGroupings,
		StopIds:       allStopsIds,
	}

	return result, stopsList, nil
}

func buildStopsList(ctx context.Context, api *RestAPI, calc *GTFS.AdvancedDirectionCalculator, agencyID string, allStops map[string]bool) ([]models.Stop, error) {

	stopIDs := make([]string, 0, len(allStops))
	for stopID := range allStops {
		stopIDs = append(stopIDs, stopID)
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	routeRows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Organize Routes in Memory
	routesMap := make(map[string][]string)
	for _, row := range routeRows {
		routeID, ok := row.RouteID.(string)
		stopID := row.StopID

		if ok {
			routesMap[stopID] = append(routesMap[stopID], routeID)
		}
	}

	stopsList := make([]models.Stop, 0, len(stops))

	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		direction := calc.CalculateStopDirection(ctx, stop.ID, stop.Direction)

		routeIdsString := append([]string(nil), routesMap[stop.ID]...)

		stopsList = append(stopsList, models.Stop{
			Code:               stop.Code.String,
			Direction:          direction,
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Lat:                stop.Lat,
			LocationType:       int(stop.LocationType.Int64),
			Lon:                stop.Lon,
			Name:               stop.Name.String,
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}
	return stopsList, nil
}

func (api *RestAPI) buildAndSendResponse(w http.ResponseWriter, r *http.Request, ctx context.Context, result models.RouteEntry, stopsList []models.Stop, currentAgency gtfsdb.Agency) {
	agencyRef := models.NewAgencyReference(
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

	routeRefs, err := api.BuildRouteReferencesAsInterface(ctx, currentAgency.ID, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references := models.ReferencesModel{
		Agencies:   []models.AgencyReference{agencyRef},
		Routes:     routeRefs,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      stopsList,
		Trips:      []interface{}{},
	}

	response := models.NewEntryResponse(result, references, api.Clock)
	api.sendResponse(w, r, response)
}

func processTripGroups(
	ctx context.Context,
	api *RestAPI,
	agencyID string,
	routeID string,
	trips []gtfsdb.Trip,
	stopGroupings *[]models.StopGrouping,
	allStops map[string]bool,
	allPolylines *[]models.Polyline,
) {
	type directionHeadsignKey struct {
		DirectionID  int64
		TripHeadsign string
	}

	tripGroups := make(map[directionHeadsignKey][]gtfsdb.Trip)
	for _, trip := range trips {
		key := directionHeadsignKey{
			DirectionID:  trip.DirectionID.Int64,
			TripHeadsign: trip.TripHeadsign.String,
		}
		tripGroups[key] = append(tripGroups[key], trip)
	}

	var allStopGroups []models.StopGroup

	var keys []directionHeadsignKey
	for key := range tripGroups {
		keys = append(keys, key)
	}

	/// Sort by direction ID to ensure consistent ordering (0 = outbound, 1 = inbound)
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].DirectionID < keys[j].DirectionID
	})

	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}

		tripsInGroup := tripGroups[key]

		// Sort trips by ID to ensure we always pick the same representative trip
		sort.Slice(tripsInGroup, func(i, j int) bool {
			return tripsInGroup[i].ID < tripsInGroup[j].ID
		})

		representativeTrip := tripsInGroup[0]
		stopsList, err := api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, representativeTrip.ID)
		if err != nil {
			continue
		}

		stopIDs := make(map[string]bool)
		for _, stopID := range stopsList {
			stopIDs[stopID] = true
			allStops[stopID] = true
		}

		shape, err := api.GtfsManager.GtfsDB.Queries.GetShapesGroupedByTripHeadSign(ctx,
			gtfsdb.GetShapesGroupedByTripHeadSignParams{
				RouteID:      routeID,
				TripHeadsign: representativeTrip.TripHeadsign,
			})
		if err != nil {
			continue
		}

		polylines := generatePolylines(shape)
		*allPolylines = append(*allPolylines, polylines...)

		formattedStopIDs := formatStopIDs(agencyID, stopIDs)

		groupID := fmt.Sprintf("%d", key.DirectionID-1)

		stopGroup := models.StopGroup{
			ID: groupID,
			Name: models.StopGroupName{
				Name:  key.TripHeadsign,
				Names: []string{key.TripHeadsign},
				Type:  "destination",
			},
			StopIds:   formattedStopIDs,
			Polylines: polylines,
		}

		allStopGroups = append(allStopGroups, stopGroup)
	}

	if len(allStopGroups) > 0 {
		*stopGroupings = append(*stopGroupings, models.StopGrouping{
			Ordered:    true,
			StopGroups: allStopGroups,
			Type:       "direction",
		})
	}
}

func generatePolylines(shapes []gtfsdb.GetShapesGroupedByTripHeadSignRow) []models.Polyline {
	var polylines []models.Polyline
	// This prevents repeated memory re-allocation during the loop.
	coords := make([][]float64, 0, len(shapes))
	for _, shape := range shapes {
		coords = append(coords, []float64{shape.Lat, shape.Lon})
	}
	encodedPoints := polyline.EncodeCoords(coords)
	polylines = append(polylines, models.Polyline{
		Length: len(shapes),
		Levels: "",
		Points: string(encodedPoints),
	})
	return polylines
}

func formatStopIDs(agencyID string, stops map[string]bool) []string {
	var stopIDs []string
	for key := range stops {
		stopID := utils.FormCombinedID(agencyID, key)
		stopIDs = append(stopIDs, stopID)
	}
	return stopIDs
}
