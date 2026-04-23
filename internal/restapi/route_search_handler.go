package restapi

import (
	"net/http"
	"strings"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// routeSearchHandler searches for routes matching a user-provided query string,
// with optional geographic bounds filtering via lat, lon, and radius parameters.
func (api *RestAPI) routeSearchHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	input := queryParams.Get("input")
	sanitizedInput, err := utils.ValidateAndSanitizeQuery(input)
	if err != nil {
		fieldErrors := map[string][]string{
			"input": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	if strings.TrimSpace(sanitizedInput) == "" {
		fieldErrors := map[string][]string{
			"input": {"input is required"},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// maxCount defaults to 20
	maxCount := 20
	var fieldErrors map[string][]string
	if maxCountStr := queryParams.Get("maxCount"); maxCountStr != "" {
		parsedMaxCount, fe := utils.ParseFloatParam(queryParams, "maxCount", fieldErrors)
		fieldErrors = fe
		if parsedMaxCount <= 0 {
			fieldErrors["maxCount"] = append(fieldErrors["maxCount"], "must be greater than zero")
		} else {
			maxCount = int(parsedMaxCount)
			if maxCount > 100 {
				fieldErrors["maxCount"] = append(fieldErrors["maxCount"], "must not exceed 100")
			}
		}
	}

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	routes, err := api.GtfsManager.SearchRoutes(ctx, sanitizedInput, maxCount)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

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

	response := models.NewListResponseWithRange(results, *references, false, api.Clock, false)
	api.sendResponse(w, r, response)
}
