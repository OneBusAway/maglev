package restapi

import (
	"errors"
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// agencyHandler returns details for a single transit agency identified by its path ID.
func (api *RestAPI) agencyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	// Protect against nil pointer panics if the DB fails to load
	if api.GtfsManager == nil {
		api.serverErrorResponse(w, r, errors.New("GTFS database is currently unavailable"))
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
