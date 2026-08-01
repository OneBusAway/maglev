package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/utils"
)

// extractAndValidateID extracts the ID from the URL, validates it, and handles errors
func (api *RestAPI) extractAndValidateID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(id); err != nil {
		fieldErrors := map[string][]string{"id": {err.Error()}}
		api.validationErrorResponse(w, r, fieldErrors)
		return "", false
	}

	return id, true
}

// extractAndValidateAgencyCodeID extracts and validates a combined agency_code ID from the URL
func (api *RestAPI) extractAndValidateAgencyCodeID(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return "", "", false
	}

	agencyID, codeID, err := utils.ExtractAgencyIDAndCodeID(id)
	if err != nil {
		fieldErrors := map[string][]string{"id": {err.Error()}}
		api.validationErrorResponse(w, r, fieldErrors)
		return "", "", false
	}

	return agencyID, codeID, true
}
