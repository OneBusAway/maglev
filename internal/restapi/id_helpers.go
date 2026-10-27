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

// extractAgencyCodeID reads a combined agency_code id. A non-empty queryParam
// wins over the path: Java applies the path id first and the query string
// after it, and setStopId/setTripId call setId, so
// /report-problem-with-stop/1_A.json?stopId=1_B stores B. The path id is the
// fallback when that query parameter is absent. Android submits
// report-problem-with-stop.json?stopId= and
// report-problem-with-trip.json?tripId= with no path id. Validation failures
// use the same rules as a path id.
func (api *RestAPI) extractAgencyCodeID(w http.ResponseWriter, r *http.Request, queryParam string) (string, string, bool) {
	id := ""
	field := "id"
	if queryParam != "" {
		field = queryParam
		if q := r.URL.Query().Get(queryParam); q != "" {
			id = q
		}
	}
	if id == "" {
		if pathID := utils.ExtractIDFromParams(r); pathID != "" {
			id = pathID
			field = "id"
		}
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
