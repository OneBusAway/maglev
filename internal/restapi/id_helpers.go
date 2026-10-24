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

// requireStopAgency sends not found unless agencyID is the agency in the stop's ID,
// and reports whether the request may go on. Java OneBusAway gives each stop one ID,
// under its feed's first agency, so the stop doesn't exist under any other prefix,
// even an agency whose routes serve it.
func (api *RestAPI) requireStopAgency(w http.ResponseWriter, r *http.Request, agencyID, stopID string) bool {
	stopAgencyID, err := api.GtfsManager.GtfsDB.Queries.GetStopAgencyID(r.Context(), stopID)
	if err != nil {
		api.dbErrorResponse(w, r, err)
		return false
	}
	if stopAgencyID != agencyID {
		api.sendNotFound(w, r)
		return false
	}
	return true
}
