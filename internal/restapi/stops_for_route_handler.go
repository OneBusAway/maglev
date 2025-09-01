package restapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/OneBusAway/go-gtfs"
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
	now := time.Now()
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
	agencyID, routeID, _ := utils.ExtractAgencyIDAndCodeID(utils.ExtractIDFromParams(r))

	ctx := r.Context()

	_, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	params := api.parseStopsForRouteParams(r)

	currentAgency := api.handleCommonErrors(w, r, agencyID, routeID)
	if currentAgency == nil {
		return
	}

	currentLocation, _ := time.LoadLocation(currentAgency.Timezone)
	timeParam := r.URL.Query().Get("time")

	formattedDate, _, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
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

	api.buildAndSendResponse(w, r, ctx, result, stopsList, *currentAgency)
}

func (api *RestAPI) handleCommonErrors(w http.ResponseWriter, r *http.Request, agencyID string, routeID string) *gtfs.Agency {
	if routeID == "" || agencyID == "" {
		http.Error(w, "null", http.StatusInternalServerError)
		return nil
	}

	currentAgency := api.GtfsManager.FindAgency(agencyID)
	if currentAgency == nil {
		http.Error(w, "null", http.StatusInternalServerError)
		return nil
	}

	return currentAgency
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
		processTripGroups(ctx, api, agencyID, routeID, allTrips, &stopGroupings, allStops, &allPolylines)
	} else {
		// Process trips for the current service date
		processTripGroups(ctx, api, agencyID, routeID, trips, &stopGroupings, allStops, &allPolylines)
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
	stopsList := make([]models.Stop, 0, len(allStops))
	for stopID := range allStops {
		stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
		if err != nil {
			continue
		}

		routeIds, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStop(ctx, stop.ID)
		if err != nil {
			continue
		}

		routeIdsString := make([]string, len(routeIds))
		for i, id := range routeIds {
			routeIdsString[i] = id.(string)
		}

		stopsList = append(stopsList, models.Stop{
			Code:               stop.Code.String,
			Direction:          "Direction", // TODO calculate stop direction
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Lat:                stop.Lat,
			LocationType:       int(stop.LocationType.Int64),
			Lon:                stop.Lon,
			Name:               stop.Name.String,
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(gtfs.WheelchairBoarding(stop.WheelchairBoarding.Int64)),
		})
	}
	return stopsList, nil
}

func (api *RestAPI) buildAndSendResponse(w http.ResponseWriter, r *http.Request, ctx context.Context, result models.RouteEntry, stopsList []models.Stop, currentAgency gtfs.Agency) {
	agencyRef := models.NewAgencyReference(
		currentAgency.Id,
		currentAgency.Name,
		currentAgency.Url,
		currentAgency.Timezone,
		currentAgency.Language,
		currentAgency.Phone,
		currentAgency.Email,
		currentAgency.FareUrl,
		"",
		false,
	)

	routeRefs, err := api.BuildRouteReferencesAsInterface(ctx, currentAgency.Id, stopsList)
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

	response := models.NewEntryResponse(result, references)
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
		tripsInGroup := tripGroups[key]
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
	var coords [][]float64
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
