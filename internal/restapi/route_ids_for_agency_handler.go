package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) routeIDsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id := utils.ExtractIDFromParams(r)

	// Acquire read lock
	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	agency := api.GtfsManager.FindAgency(id)

	if agency == nil {
		api.sendNull(w, r)
		return
	}

	ctx := r.Context()

	routeIDs, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForAgency(ctx, id)

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	response := make([]string, 0, len(routeIDs))
	for _, routeID := range routeIDs {
		response = append(response, utils.FormCombinedID(id, routeID))
	}

	api.sendResponse(w, r, models.NewListResponse(response, models.NewEmptyReferences(), api.Clock))
}
