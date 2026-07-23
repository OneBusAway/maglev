package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// routesForAgencyHandler returns all routes operated by a given agency.
func (api *RestAPI) routesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	agency, err := api.GtfsManager.FindAgency(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	if agency == nil {
		api.sendNotFound(w, r)
		return
	}

	routesForAgency, err := api.GtfsManager.RoutesForAgencyID(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routesList := make([]models.Route, 0, len(routesForAgency))

	for _, route := range routesForAgency {
		routesList = append(routesList, models.NewRoute(
			utils.FormCombinedID(agency.ID, route.ID),
			agency.ID,
			nulls.StringOrEmpty(route.ShortName),
			nulls.StringOrEmpty(route.LongName),
			nulls.StringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			nulls.StringOrEmpty(route.Url),
			nulls.StringOrEmpty(route.Color),
			nulls.StringOrEmpty(route.TextColor)))
	}

	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{
		models.AgencyReferenceFromDatabase(agency),
	}

	// Spec: this endpoint returns all matching routes, so limitExceeded is always false.
	response := models.NewListResponse(routesList, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}
