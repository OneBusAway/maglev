package restapi

import (
	"fmt"
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// routeIDsForAgencyHandler returns a list of route IDs belonging to a given agency.
func (api *RestAPI) routeIDsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	if v := r.URL.Query().Get("version"); v != "" && v != "2" {
		api.sendError(w, r, http.StatusBadRequest, fmt.Sprintf("unknown version: %s", v))
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

	routeIDs, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForAgency(ctx, id)

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	response := make([]string, 0, len(routeIDs))
	for _, routeID := range routeIDs {
		response = append(response, utils.FormCombinedID(id, routeID))
	}

	api.sendResponse(w, r, models.NewListResponse(response, *models.NewEmptyReferences(), false, api.Clock))
}
