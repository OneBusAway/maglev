package restapi

import (
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
