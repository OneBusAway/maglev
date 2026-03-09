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

	loc := api.parseLocationParams(w, r)
	if loc == nil {
		return
	}

	maxCount, fieldErrors := utils.ParseMaxCount(queryParams, models.DefaultMaxCountForRoutes, nil)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	query := queryParams.Get("query")

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
	radius := loc.Radius
	if radius == 0 {
		radius = models.DefaultSearchRadiusInMeters
		if query != "" {
			radius = models.QuerySearchRadiusInMeters
		}
	}

	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	stops := api.GtfsManager.GetStopsForLocation(ctx, loc.Lat, loc.Lon, radius, loc.LatSpan, loc.LonSpan, query, maxCount, true, nil, time.Time{})

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
		references := models.NewEmptyReferences()
		references.Agencies = agencies
		response := models.NewListResponseWithRange(results, references, checkIfOutOfBounds(api, loc.Lat, loc.Lon, loc.LatSpan, loc.LonSpan, radius), api.Clock, false)
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
	var resultRawRouteIDs []string
	// Process routes and filter by query if provided
	for _, routeRow := range routesForStops {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		if query != "" && strings.ToLower(routeRow.ShortName.String) != query {
			continue
		}

		combinedRouteID := utils.FormCombinedID(routeRow.AgencyID, routeRow.ID)

		if !routeIDs[combinedRouteID] {
			agencyIDs[routeRow.AgencyID] = true
			resultRawRouteIDs = append(resultRawRouteIDs, routeRow.ID)

			results = append(results, models.NewRoute(
				combinedRouteID,
				routeRow.AgencyID,
				routeRow.ShortName.String,
				routeRow.LongName.String,
				routeRow.Desc.String,
				models.RouteType(routeRow.Type),
				routeRow.Url.String,
				routeRow.Color.String,
				routeRow.TextColor.String))
		}
		routeIDs[combinedRouteID] = true
		if len(results) >= maxCount {
			isLimitExceeded = true
			break
		}
	}

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)

	// Populate situation references for alerts affecting the returned routes
	alerts := api.collectAlertsForRoutes(resultRawRouteIDs)
	situations := api.BuildSituationReferences(alerts)

	references := models.NewEmptyReferences()
	references.Agencies = agencies
	references.Situations = situations

	response := models.NewListResponseWithRange(results, references, checkIfOutOfBounds(api, loc.Lat, loc.Lon, loc.LatSpan, loc.LonSpan, radius), api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}

// checkIfOutOfBounds returns true if the user's search area is completely
// outside the transit agency's region bounds (derived from shape data).
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func checkIfOutOfBounds(api *RestAPI, lat float64, lon float64, latSpan float64, lonSpan float64, radius float64) bool {
	regionLat, regionLon, regionLatSpan, regionLonSpan := api.GtfsManager.GetRegionBounds()

	// returns false if there exists only one point
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
