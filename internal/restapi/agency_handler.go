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

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	agency := api.GtfsManager.FindAgency(id)

	if agency == nil {
		api.sendNotFound(w, r)
		return
	}

	agencyData := models.NewAgencyReference(
		agency.Id,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Language,
		agency.Phone,
		agency.Email,
		agency.FareUrl,
		"",
		false,
	)

	response := models.NewEntryResponse(agencyData, *models.NewEmptyReferences(), api.Clock)
	api.sendResponse(w, r, response)
}
