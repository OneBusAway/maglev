package restapi

import (
	"maps"
	"net/http"
	"slices"
	"strings"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// routesForLocationHandler returns routes serving stops near a geographic location,
// specified by lat/lon coordinates with an optional radius or latSpan/lonSpan bounding box.
func (api *RestAPI) routesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	var fieldErrors map[string][]string
	loc, fieldErrors := api.parseLocationParams(r, fieldErrors)
	maxCount, fieldErrors := utils.ParseMaxCountClamped(queryParams, models.DefaultMaxCountForRoutesForLocation, fieldErrors)

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// The spec caps this endpoint below the global maxCount ceiling.
	maxCount = min(maxCount, models.MaxCountForRoutesForLocation)

	query := utils.SanitizeInput(queryParams.Get("query"))

	// Both spans must be positive for BoundsFromParams to size the box from them;
	// otherwise it falls back to a radius, which is only query-aware here.
	hasSpanBounds := loc.LatSpan > 0 && loc.LonSpan > 0
	needsDefaultRadius := loc.Radius == 0 && !hasSpanBounds
	if needsDefaultRadius {
		loc.Radius = models.DefaultSearchRadiusInMeters
		if query != "" {
			loc.Radius = models.QuerySearchRadiusInMeters
		}
	}

	ctx := r.Context()
	routes, isLimitExceeded := api.GtfsManager.GetRoutesForLocation(ctx, loc, query, maxCount)
	if len(routes) == 0 {
		references := models.NewEmptyReferences()
		response := models.NewListResponseWithRange([]models.Route{}, *references, api.GtfsManager.CheckIfOutOfBounds(loc), api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	var results []models.Route
	agencyIDs := map[string]bool{}
	for _, route := range routes {
		agencyIDs[route.AgencyID] = true
		results = append(results, models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			nulls.StringOrEmpty(route.ShortName),
			nulls.StringOrEmpty(route.LongName),
			nulls.StringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			nulls.StringOrEmpty(route.Url),
			nulls.StringOrEmpty(route.Color),
			nulls.StringOrEmpty(route.TextColor)))
	}

	references := models.NewEmptyReferences()
	// Only agencies are referenced here: route beans carry no situation IDs, so
	// references.situations stays empty even when alerts affect the returned routes.
	// When includeReferences=false the references block is present but empty.
	if ShouldIncludeReferences(r) {
		agencyIDList := slices.Collect(maps.Keys(agencyIDs))
		agencies, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(ctx, agencyIDList)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Agencies = buildAgencyReferences(agencies)
	}

	// Results must be sorted by ID after maxCount limit is applied.
	// See how response changes when calling java API with different maxCounts.
	slices.SortFunc(results, func(a, b models.Route) int {
		return strings.Compare(a.ID, b.ID)
	})
	response := models.NewListResponseWithRange(results, *references, api.GtfsManager.CheckIfOutOfBounds(loc), api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}
