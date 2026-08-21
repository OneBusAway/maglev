package restapi

import (
	"net/http"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// situationHandler serves a single GTFS-RT service alert (OneBusAway "Situation")
// by its alert id.
func (api *RestAPI) situationHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, codeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}
	requestID := utils.FormCombinedID(agencyID, codeID)

	var alert gtfs.Alert
	found := false
	for _, candidate := range api.GtfsManager.GetAllAlerts() {
		if situationAlertMatches(candidate, requestID) {
			alert = candidate
			found = true
			break
		}
	}
	if !found {
		api.sendNotFound(w, r)
		return
	}

	situations := api.BuildSituationReferences([]gtfs.Alert{alert})
	situations[0].ID = situationID(alert.ID, agencyIDForAlert(alert, ""))
	references := models.NewEmptyReferences()
	response := models.NewEntryResponse(situations[0], *references, api.Clock)
	api.sendResponse(w, r, response)
}

// situationAlertMatches reports whether a GTFS-RT alert is the situation named
// by requestID. Clients following situationIds from trip-details use the
// agency-qualified form; some feeds already store that form as alert.ID.
func situationAlertMatches(alert gtfs.Alert, requestID string) bool {
	if alert.ID == requestID {
		return true
	}
	return situationID(alert.ID, agencyIDForAlert(alert, "")) == requestID
}
