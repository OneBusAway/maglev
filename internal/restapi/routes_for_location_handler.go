package restapi

import (
	"net/http"
	"strings"
	"time"

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
	maxCount, _ := utils.ParseMaxCount(queryParams, models.DefaultMaxCountForRoutes, fieldErrors)
	query := queryParams.Get("query")

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Validate location parameters
	locationErrors := utils.ValidateLocationParams(lat, lon, radius, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		return
	}

	// Validate and sanitize query
	sanitizedQuery, err := utils.ValidateAndSanitizeQuery(query)
	if err != nil {
		fieldErrors := map[string][]string{
			"query": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	query = strings.ToLower(sanitizedQuery)
	if radius == 0 {
		radius = models.DefaultSearchRadiusInMeters
		if query != "" {
			radius = models.QuerySearchRadiusInMeters
		}
	}

	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return
	}

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	stops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, radius, latSpan, lonSpan, query, maxCount, true, nil, time.Time{})

	var results = []models.Route{}
	routeIDs := map[string]bool{}
	agencyIDs := map[string]bool{}

	// Extract stop IDs for batch query
	stopIDs := make([]string, 0, len(stops))
	for _, stop := range stops {
		stopIDs = append(stopIDs, stop.ID)
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
		response := models.NewListResponseWithRange(results, references, checkIfOutOfBounds(api, lat, lon, latSpan, lonSpan, radius), api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	// Batch query to get all routes for all stops
	routesForStops, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	isLimitExceeded := false
	// Process routes and filter by query if provided
	for _, routeRow := range routesForStops {
		if ctx.Err() != nil {
			return
		}

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
		if len(results) >= maxCount {
			isLimitExceeded = true
			break
		}
	}

	if ctx.Err() != nil {
		return
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

	response := models.NewListResponseWithRange(results, references, checkIfOutOfBounds(api, lat, lon, latSpan, lonSpan, radius), api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}

// checkIfOutOfBounds returns true if the user's search area is completely
// outside the transit agency's region bounds (derived from shape data).
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func checkIfOutOfBounds(api *RestAPI, lat float64, lon float64, latSpan float64, lonSpan float64, radius float64) bool {
	regionLat, regionLon, regionLatSpan, regionLonSpan := api.GtfsManager.GetRegionBounds()

	// TODO: use stop locations data as a fallback if no shapes exists
	// returns false if there is no shapes or there exists only one point
	if regionLatSpan == 0 && regionLonSpan == 0 {
		return false
	}

	innerBounds := utils.CalculateBounds(lat, lon, radius)

	if latSpan > 0 && lonSpan > 0 {
		innerBounds = utils.CalculateBoundsFromSpan(lat, lon, latSpan/2, lonSpan/2)
	}

	outerBounds := utils.CalculateBoundsFromSpan(regionLat, regionLon, regionLatSpan/2, regionLonSpan/2)

	return utils.IsOutOfBounds(innerBounds, outerBounds)
}
