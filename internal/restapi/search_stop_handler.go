package restapi

import (
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) searchStopHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	input := queryParams.Get("input")
	if input == "" {
		fieldErrors := map[string][]string{
			"input": {"input is required"},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Parse maxCount parameter (default 20, max 250)
	maxCount := 20
	fieldErrors := make(map[string][]string)
	if maxCountStr := queryParams.Get("maxCount"); maxCountStr != "" {
		parsedMaxCount, err := utils.ParseFloatParam(queryParams, "maxCount", fieldErrors)
		if err == nil {
			maxCount = int(parsedMaxCount)
			if maxCount <= 0 {
				fieldErrors["maxCount"] = []string{"must be greater than zero"}
			} else if maxCount > 250 {
				fieldErrors["maxCount"] = []string{"must not exceed 250"}
			}
		}
	}

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	// Execute Search
	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStops(ctx, gtfsdb.SearchStopsParams{
		SearchQuery: input,
		Limit:       int64(maxCount),
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// If no stops found, return empty list immediately
	if len(stops) == 0 {
		response := models.NewListResponseWithRange([]models.Stop{}, models.NewEmptyReferences(), false)
		api.sendResponse(w, r, response)
		return
	}

	// Collect Stop IDs for batch fetching references
	stopIDs := make([]string, len(stops))
	stopMap := make(map[string]gtfsdb.Stop)
	for i, stop := range stops {
		stopIDs[i] = stop.ID
		stopMap[stop.ID] = stop
	}

	// Batch query to get route IDs for all stops
	routeIDsForStops, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Batch query to get agencies for all stops
	agenciesForStops, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Create maps for efficient lookup
	stopRouteIDs := make(map[string][]string)
	stopAgency := make(map[string]*gtfsdb.GetAgenciesForStopsRow)
	routeIDs := map[string]bool{}
	agencyIDs := map[string]bool{}

	for _, routeIDRow := range routeIDsForStops {
		stopID := routeIDRow.StopID
		routeIDStr, ok := routeIDRow.RouteID.(string)
		if !ok {
			continue
		}

		agencyId, routeId, _ := utils.ExtractAgencyIDAndCodeID(routeIDStr)
		stopRouteIDs[stopID] = append(stopRouteIDs[stopID], routeIDStr)
		agencyIDs[agencyId] = true
		routeIDs[routeId] = true
	}

	for _, agencyRow := range agenciesForStops {
		stopID := agencyRow.StopID
		if _, exists := stopAgency[stopID]; !exists {
			stopAgency[stopID] = &agencyRow
		}
	}

	// Build Result List
	var results []models.Stop
	for _, stop := range stops {
		rids := stopRouteIDs[stop.ID]
		agency := stopAgency[stop.ID]

		// Skip stops that don't have valid agency/route info (data consistency)
		if agency == nil {
			continue
		}

		direction := models.UnknownValue
		if stop.Direction.Valid && stop.Direction.String != "" {
			direction = stop.Direction.String
		}

		results = append(results, models.NewStop(
			utils.NullStringOrEmpty(stop.Code),
			direction,
			utils.FormCombinedID(agency.ID, stop.ID),
			utils.NullStringOrEmpty(stop.Name),
			"", // Parent not currently supported
			utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
			stop.Lat,
			stop.Lon,
			int(stop.LocationType.Int64),
			rids,
			rids, // StaticRouteIDs same as RouteIDs for now
		))
	}

	// Build References
	agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)
	routes := utils.FilterRoutes(api.GtfsManager.GtfsDB.Queries, ctx, routeIDs)

	references := models.ReferencesModel{
		Agencies:   agencies,
		Routes:     routes,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	response := models.NewListResponseWithRange(results, references, false)
	api.sendResponse(w, r, response)
}
