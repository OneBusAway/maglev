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

	includeReferences := r.URL.Query().Get("includeReferences") != "false"

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

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
			utils.NullStringOrEmpty(route.ShortName),
			utils.NullStringOrEmpty(route.LongName),
			utils.NullStringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			utils.NullStringOrEmpty(route.Url),
			utils.NullStringOrEmpty(route.Color),
			utils.NullStringOrEmpty(route.TextColor)))
	}

	references := models.NewEmptyReferences()
	if includeReferences {
		references.Agencies = []models.AgencyReference{
			models.AgencyReferenceFromDatabase(agency),
		}
	}

	response := models.NewListResponse(routesList, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}
