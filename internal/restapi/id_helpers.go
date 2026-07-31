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

// resolveAgencyID determines which agency a stop belongs to. Stops carry no agency of
// their own, so the agency is taken from the first route serving the stop, falling back
// to the only agency present when the surrounding result set spans just one. Returns an
// empty string when the agency cannot be determined.
func resolveAgencyID(stopID string, routesByStop map[string][]string, uniqueAgencies map[string]bool) string {
	if routes, ok := routesByStop[stopID]; ok && len(routes) > 0 {
		agencyID, _, _ := utils.ExtractAgencyIDAndCodeID(routes[0])
		return agencyID
	}
	if len(uniqueAgencies) == 1 {
		for id := range uniqueAgencies {
			return id
		}
	}
	return ""
}

// formatStopID combines a raw stop ID with its agency, returning the raw ID unchanged
// when the agency is unknown and an empty string when there is no ID to format.
func formatStopID(agencyID, rawID string) string {
	if rawID == "" {
		return ""
	}
	if agencyID == "" {
		return rawID
	}
	return utils.FormCombinedID(agencyID, rawID)
}
