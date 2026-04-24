package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// agencyHandler returns details for a single transit agency identified by its path ID.
func (api *RestAPI) agencyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	agency, err := api.GtfsManager.FindAgency(r.Context(), id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	if agency == nil {
		api.sendNotFound(w, r)
		return
	}

	agencyData := models.AgencyReferenceFromDatabase(agency)
	// NewEntryResponse naturally defaults to the v2 response envelope format
	response := models.NewEntryResponse(agencyData, *models.NewEmptyReferences(), api.Clock)
	api.sendResponse(w, r, response)
}
