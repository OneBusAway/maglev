package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// routeSearchHandler searches for routes matching a user-provided query string,
// with optional geographic bounds filtering via lat, lon, and radius parameters.
func (api *RestAPI) routeSearchHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()
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

	if len(routes) == 0 {
		api.sendNotFound(w, r)
		return
	}

	routes, isLimitExceeded := utils.PaginateSlice(routes, 0, maxCount)

	results := make([]models.Route, 0, len(routes))
	agencyIDs := make(map[string]bool)
	for _, routeRow := range routes {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		agencyIDs[routeRow.AgencyID] = true

		shortName := ""
		if routeRow.ShortName.Valid {
			shortName = routeRow.ShortName.String
		}
		longName := ""
		if routeRow.LongName.Valid {
			longName = routeRow.LongName.String
		}
		desc := ""
		if routeRow.Desc.Valid {
			desc = routeRow.Desc.String
		}
		url := ""
		if routeRow.Url.Valid {
			url = routeRow.Url.String
		}
		color := ""
		if routeRow.Color.Valid {
			color = routeRow.Color.String
		}
		textColor := ""
		if routeRow.TextColor.Valid {
			textColor = routeRow.TextColor.String
		}

		results = append(results, models.NewRoute(
			utils.FormCombinedID(routeRow.AgencyID, routeRow.ID),
			routeRow.AgencyID,
			shortName,
			longName,
			desc,
			models.RouteType(routeRow.Type),
			url,
			color,
			textColor))
	}

	allAgencies, err := api.GtfsManager.GetAgencies(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	agencies := utils.FilterAgencies(allAgencies, agencyIDs)
	// Populate situation references for alerts affecting the returned routes
	resultRawRouteIDs := make([]string, 0, len(routes))
	for _, routeRow := range routes {
		resultRawRouteIDs = append(resultRawRouteIDs, routeRow.ID)
	}
	alerts := api.collectAlertsForRoutes(resultRawRouteIDs)
	situations := api.BuildSituationReferences(alerts)

	references := models.NewEmptyReferences()
	references.Agencies = agencies
	references.Situations = situations

	response := models.NewListResponseWithRange(results, *references, false, api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}
