package restapi

import (
	"maps"
	"net/http"
	"slices"
	"strings"
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
	if loc.Radius == 0 {
		loc.Radius = models.DefaultSearchRadiusInMeters
		if query != "" {
			loc.Radius = models.QuerySearchRadiusInMeters
		}
	}

	ctx := r.Context()
	routes, isLimitExceeded := api.GtfsManager.GetRoutesForLocation(ctx, loc, sanitizedQuery, maxCount, time.Time{})
	if len(routes) == 0 {
		references := models.NewEmptyReferences()
		response := models.NewListResponseWithRange([]models.Route{}, *references, api.GtfsManager.CheckIfOutOfBounds(loc), api.Clock, false)
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

	references := models.NewEmptyReferences()

	agencyIDList := slices.Collect(maps.Keys(agencyIDs))
	agencies, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(ctx, agencyIDList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	references.Agencies = buildAgencyReferences(agencies)

	// Populate situation references for alerts affecting the returned routes
	alerts := api.collectAlertsForRoutes(slices.Collect(maps.Keys(routeIDs)))
	references.Situations = api.BuildSituationReferences(alerts)

	// Results must be sorted by ID after maxCount limit is applied.
	// See how response changes when calling java API with different maxCounts.
	slices.SortFunc(results, func(a, b models.Route) int {
		return strings.Compare(a.ID, b.ID)
	})
	response := models.NewListResponseWithRange(results, *references, api.GtfsManager.CheckIfOutOfBounds(loc), api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}
