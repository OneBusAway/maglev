package restapi

import (
	"context"
	"net/http"
	"strings"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) routesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	lat, fieldErrors := utils.ParseFloatParam(queryParams, "lat", nil)
	lon, _ := utils.ParseFloatParam(queryParams, "lon", fieldErrors)
	radius, _ := utils.ParseFloatParam(queryParams, "radius", fieldErrors)
	latSpan, _ := utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, _ := utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)
	query := queryParams.Get("query")
	query = strings.ToLower(query)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	if radius == 0 {
		// Default radius to 600 meters if not specified
		radius = 600
		if query != "" {
			radius = 10000
		}
	}

	stops := api.GtfsManager.GetStopsForLocation(lat, lon, radius, latSpan, lonSpan, query, 50, true)

	ctx := context.Background()
	var results = []models.Route{}
	routeIDs := map[string]bool{}
	agencyIDs := map[string]bool{}

	// Extract stop IDs for batch query
	stopIDs := make([]string, 0, len(stops))
	for _, stop := range stops {
		stopIDs = append(stopIDs, stop.Id)
	}

	if len(stopIDs) == 0 {
		// Return empty response if no stops found
		agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)
		references := models.ReferencesModel{
			Agencies:   agencies,
			Routes:     []interface{}{},
			Situations: []interface{}{},
			StopTimes:  []interface{}{},
			Stops:      []models.Stop{},
			Trips:      []interface{}{},
		}
		response := models.NewListResponseWithRange(results, references, true)
		api.sendResponse(w, r, response)
		return
	}

	// Batch query to get all routes for all stops
	routesForStops, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Process routes and filter by query if provided
	for _, routeRow := range routesForStops {
		if query != "" && strings.ToLower(routeRow.ShortName.String) != query {
			continue
		}
		agencyIDs[routeRow.AgencyID] = true
		if !routeIDs[routeRow.ID] {
			results = append(results, models.NewRoute(
				utils.FormCombinedID(routeRow.AgencyID, routeRow.ID),
				routeRow.AgencyID,
				routeRow.ShortName.String,
				routeRow.LongName.String,
				routeRow.Desc.String,
				models.RouteType(routeRow.Type),
				routeRow.Url.String,
				routeRow.Color.String,
				routeRow.TextColor.String,
				routeRow.ShortName.String,
			))
		}
		routeIDs[routeRow.ID] = true
	}

	agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)

	references := models.ReferencesModel{
		Agencies:   agencies,
		Routes:     []interface{}{},
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	response := models.NewListResponseWithRange(results, references, len(results) == 0)
	api.sendResponse(w, r, response)
}
