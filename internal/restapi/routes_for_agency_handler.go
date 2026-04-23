package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
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
		api.sendNull(w, r)
		return
	}

	routesForAgency, err := api.GtfsManager.RoutesForAgencyID(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Apply pagination
	offset, limit := utils.ParsePaginationParams(r)
	routesForAgency, limitExceeded := utils.PaginateSlice(routesForAgency, offset, limit)
	routesList := make([]models.Route, 0, len(routesForAgency))

	for _, route := range routesForAgency {
		routesList = append(routesList, models.NewRoute(
			utils.FormCombinedID(agency.ID, route.ID),
			agency.ID,
			utils.NullStringOrEmpty(route.ShortName),
			utils.NullStringOrEmpty(route.LongName),
			utils.NullStringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			utils.NullStringOrEmpty(route.Url),
			utils.NullStringOrEmpty(route.Color),
			utils.NullStringOrEmpty(route.TextColor)))
	}

	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{
		models.AgencyReferenceFromDatabase(agency),
	}

	response := models.NewListResponse(routesList, *references, limitExceeded, api.Clock)
	api.sendResponse(w, r, response)
}
