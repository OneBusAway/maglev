package restapi

import (
	"maps"
	"net/http"
	"slices"
	"time"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// routesForLocationHandler returns routes serving stops near a geographic location,
// specified by lat/lon coordinates with an optional radius or latSpan/lonSpan bounding box.
func (api *RestAPI) routesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	var fieldErrors map[string][]string
	loc, fieldErrors := api.parseLocationParams(r, fieldErrors)
	maxCount, fieldErrors := utils.ParseMaxCount(queryParams, models.DefaultMaxCountForRoutes, fieldErrors)

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	query := queryParams.Get("query")

	sanitizedQuery, err := utils.ValidateAndSanitizeQuery(query)
	if err != nil {
		fieldErrors := map[string][]string{
			"query": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	radius := loc.Radius
	if radius == 0 {
		radius = models.DefaultSearchRadiusInMeters
		if query != "" {
			radius = models.QuerySearchRadiusInMeters
		}
	}

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	routes, isLimitExceeded := api.GtfsManager.GetRoutesForLocation(ctx, loc.Lat, loc.Lon, radius, loc.LatSpan, loc.LonSpan, sanitizedQuery, maxCount, time.Time{})
	if len(routes) == 0 {
		references := models.NewEmptyReferences()
		response := models.NewListResponseWithRange([]models.Route{}, *references, checkIfOutOfBounds(api, loc.Lat, loc.Lon, loc.LatSpan, loc.LonSpan, radius), api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	var results []models.Route
	routeIDs := map[string]bool{}
	agencyIDs := map[string]bool{}
	for _, route := range routes {
		agencyIDs[route.AgencyID] = true
		routeIDs[route.ID] = true
		results = append(results, models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			utils.NullStringOrEmpty(route.ShortName),
			utils.NullStringOrEmpty(route.LongName),
			utils.NullStringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			utils.NullStringOrEmpty(route.Url),
			utils.NullStringOrEmpty(route.Color),
			utils.NullStringOrEmpty(route.TextColor)))
	}

	var agencies []models.AgencyReference
	for agencyID := range agencyIDs {
		agency, err := api.GtfsManager.FindAgency(ctx, agencyID)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		agencies = append(agencies, models.AgencyReferenceFromDatabase(agency))
	}

	// Populate situation references for alerts affecting the returned routes
	alerts := api.collectAlertsForRoutes(slices.Collect(maps.Keys(routeIDs)))
	situations := api.BuildSituationReferences(alerts)

	references := models.NewEmptyReferences()
	references.Agencies = agencies
	references.Situations = situations

	response := models.NewListResponseWithRange(results, *references, checkIfOutOfBounds(api, loc.Lat, loc.Lon, loc.LatSpan, loc.LonSpan, radius), api.Clock, isLimitExceeded)
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
