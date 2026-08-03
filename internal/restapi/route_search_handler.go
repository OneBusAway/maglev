package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// routeSearchHandler searches for routes matching a user-provided query string
// using full-text search.
func (api *RestAPI) routeSearchHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()
	includeReferences := ShouldIncludeReferences(r)
	fieldErrors := make(map[string][]string)

	// Standardized parameter parsing
	query, fieldErrors := utils.ParseRequiredStringParam(queryParams, "input", fieldErrors)
	maxCount, fieldErrors := utils.ParseMaxCount(queryParams, 20, fieldErrors)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	routes, err := api.GtfsManager.SearchRoutes(ctx, query, maxCount+1)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Sort by name before truncating to maxCount so the page boundary reflects
	// natural order rather than the FTS5 relevance ranking SearchRoutes returned.
	utils.SortRoutesByName(routes)
	routes, isLimitExceeded := utils.PaginateSlice(routes, 0, maxCount)

	results := make([]models.Route, 0, len(routes))
	agencyIDs := make(map[string]bool)
	for _, routeRow := range routes {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		agencyIDs[routeRow.AgencyID] = true

		results = append(results, models.NewRoute(
			utils.FormCombinedID(routeRow.AgencyID, routeRow.ID),
			routeRow.AgencyID,
			nulls.StringOrEmpty(routeRow.ShortName),
			nulls.StringOrEmpty(routeRow.LongName),
			nulls.StringOrEmpty(routeRow.Desc),
			models.RouteType(routeRow.Type),
			nulls.StringOrEmpty(routeRow.Url),
			nulls.StringOrEmpty(routeRow.Color),
			nulls.StringOrEmpty(routeRow.TextColor)))
	}

	references := models.NewEmptyReferences()

	if includeReferences {
		allAgencies, err := api.GtfsManager.GetAgencies(ctx)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		agencies := utils.FilterAgencies(allAgencies, agencyIDs)
		utils.SortAgencyReferencesByID(agencies)

		// Populate situation references for alerts affecting the returned routes
		resultRawRouteIDs := make([]string, 0, len(routes))
		for _, routeRow := range routes {
			resultRawRouteIDs = append(resultRawRouteIDs, routeRow.ID)
		}
		alerts := api.collectAlertsForRoutes(resultRawRouteIDs)
		situations := api.BuildSituationReferences(alerts)

		references.Agencies = agencies
		references.Situations = situations
	}

	response := models.NewListResponseWithRange(results, *references, false, api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}
