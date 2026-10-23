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
	return api.extractAgencyCodeID(w, r, "")
}

// extractAgencyCodeID reads a combined agency_code id from the path. When the
// path has none and queryParam is set, it reads that query parameter instead.
// Android submits report-problem-with-stop.json?stopId= and
// report-problem-with-trip.json?tripId= with no path id; Java fills the
// action's setStopId/setTripId from the query string. A path id wins when
// both are present. Validation failures use the same rules as a path id.
func (api *RestAPI) extractAgencyCodeID(w http.ResponseWriter, r *http.Request, queryParam string) (string, string, bool) {
	id := utils.ExtractIDFromParams(r)
	field := "id"
	if id == "" && queryParam != "" {
		id = r.URL.Query().Get(queryParam)
		field = queryParam
	}

	if err := utils.ValidateID(id); err != nil {
		api.validationErrorResponse(w, r, map[string][]string{field: {err.Error()}})
		return "", "", false
	}

	agencyID, codeID, err := utils.ExtractAgencyIDAndCodeID(id)
	if err != nil {
		api.validationErrorResponse(w, r, map[string][]string{field: {err.Error()}})
		return "", "", false
	}

	return agencyID, codeID, true
}
