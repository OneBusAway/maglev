package restapi

import (
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
	"net/http"
)

func (api *RestAPI) agencyHandler(w http.ResponseWriter, r *http.Request) {
	id := utils.ExtractIDFromParams(r)

	// Validate ID
	if err := utils.ValidateID(id); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

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

	response := models.NewEntryResponse(agencyData, models.NewEmptyReferences(), api.Clock)
	api.sendResponse(w, r, response)
}
