package restapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/gtfsdb"
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

// stopsForRouteHandler returns all stops served by a route, grouped by direction
// with optional encoded polyline shapes.
func (api *RestAPI) stopsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	params := api.parseStopsForRouteParams(r)

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	currentLocation, err := loadAgencyLocation(currentAgency.ID, currentAgency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
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

	result, stopsList, err := api.processRouteStops(ctx, agencyID, routeID, serviceIDs, params.IncludePolylines)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	api.buildAndSendResponse(w, r, ctx, result, stopsList, currentAgency)
}

func (api *RestAPI) processRouteStops(ctx context.Context, agencyID string, routeID string, serviceIDs []string, includePolylines bool) (models.RouteEntry, []models.Stop, error) {
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
		if err := processTripGroups(ctx, api, agencyID, routeID, allTrips, &stopGroupings, allStops, &allPolylines); err != nil {
			return models.RouteEntry{}, nil, err
		}
	} else {
		// Process trips for the current service date
		if err := processTripGroups(ctx, api, agencyID, routeID, trips, &stopGroupings, allStops, &allPolylines); err != nil {
			return models.RouteEntry{}, nil, err
		}
	}

	if !includePolylines {
		allPolylines = []models.Polyline{}
	}

	allStopsIds := formatStopIDs(agencyID, allStops)
	stopsList, err := buildStopsList(ctx, api, agencyID, allStops)
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

func buildStopsList(ctx context.Context, api *RestAPI, agencyID string, allStops map[string]bool) ([]models.Stop, error) {

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

		direction := api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

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

	routes, err := api.BuildRouteReferences(ctx, currentAgency.ID, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{agencyRef}
	references.Routes = routes
	references.Stops = stopsList

	response := models.NewEntryResponse(result, *references, api.Clock)
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
) error {
	dirGroups := groupTripsByDirection(trips)

	var allStopGroups []models.StopGroup

	for _, group := range dirGroups {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		tripsInGroup := group.Trips

		headsignCounts := make(map[string]int)
		var dirServiceIDs []string
		seenServiceIDs := make(map[string]bool)
		for _, trip := range tripsInGroup {
			headsignCounts[trip.TripHeadsign.String]++
			if !seenServiceIDs[trip.ServiceID] {
				seenServiceIDs[trip.ServiceID] = true
				dirServiceIDs = append(dirServiceIDs, trip.ServiceID)
			}
		}

		var orderedStopIDs []string
		var err error
		if !group.DirectionID.Valid {
			/*
				direction_id is NULL in the GTFS data. SQL NULL = NULL evaluates to
				UNKNOWN, not TRUE, so GetOrderedStopIDsForRouteDirection would return
				zero rows. Fall back to single-trip ordering instead.
			*/
			orderedStopIDs, err = api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, tripsInGroup[0].ID)
		} else {
			orderedStopIDs, err = api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForRouteDirection(ctx,
				gtfsdb.GetOrderedStopIDsForRouteDirectionParams{
					RouteID:     routeID,
					DirectionID: group.DirectionID,
					ServiceIds:  dirServiceIDs,
				})
		}
		if err != nil {
			return err
		}
		for _, stopID := range orderedStopIDs {
			allStops[stopID] = true
		}

		groupHeadsign := ""
		maxCount := 0
		for headsign, count := range headsignCounts {
			if count > maxCount || (count == maxCount && headsign < groupHeadsign) {
				groupHeadsign = headsign
				maxCount = count
			}
		}

		seenHeadsigns := make(map[string]bool)
		var groupPolylines []models.Polyline
		for _, trip := range tripsInGroup {
			hs := trip.TripHeadsign.String
			if seenHeadsigns[hs] {
				continue
			}
			seenHeadsigns[hs] = true
			shape, err := api.GtfsManager.GtfsDB.Queries.GetShapesGroupedByTripHeadSign(ctx,
				gtfsdb.GetShapesGroupedByTripHeadSignParams{
					RouteID:      routeID,
					TripHeadsign: trip.TripHeadsign,
				})
			if err != nil {
				api.Logger.Warn("failed to fetch shapes for trip group", "route_id", routeID, "headsign", hs, "error", err)
				continue
			}
			pl := generatePolylines(shape)
			groupPolylines = append(groupPolylines, pl...)
		}
		*allPolylines = append(*allPolylines, groupPolylines...)

		formattedStopIDs := make([]string, len(orderedStopIDs))
		for idx, id := range orderedStopIDs {
			formattedStopIDs[idx] = utils.FormCombinedID(agencyID, id)
		}

		groupID := group.GroupID

		stopGroup := models.StopGroup{
			ID: groupID,
			Name: models.StopGroupName{
				Name:  groupHeadsign,
				Names: []string{groupHeadsign},
				Type:  "destination",
			},
			StopIds:   formattedStopIDs,
			Polylines: groupPolylines,
		}

		allStopGroups = append(allStopGroups, stopGroup)
	}

	if len(allStopGroups) > 0 {
		slices.SortFunc(*stopGroupings, func(a, b models.StopGrouping) int {
			return cmp.Compare(a.StopGroups[0].ID, b.StopGroups[0].ID)
		})

		*stopGroupings = append(*stopGroupings, models.StopGrouping{
			Ordered:    true,
			StopGroups: allStopGroups,
			Type:       "direction",
		})
	}
	return nil
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
		stopIDs = append(stopIDs, utils.FormCombinedID(agencyID, key))
	}
	slices.Sort(stopIDs)

	return stopIDs
}
