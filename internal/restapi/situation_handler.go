package restapi

import (
	"net/http"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// situationHandler returns one service alert as a situation. The id is the
// agency-qualified form other endpoints put in situationIds. A raw alert id
// is also accepted when that id is itself a combined id.
func (api *RestAPI) situationHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, codeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	alert, found := findSituationAlert(api.GtfsManager.Alerts(), agencyID, utils.FormCombinedID(agencyID, codeID))
	if !found {
		api.sendNotFound(w, r)
		return
	}

	refs := situationRefsFromAlerts([]gtfs.Alert{alert}, agencyID)
	situations := api.situationReferences(r.Context(), refs)
	api.sendResponse(w, r, models.NewEntryResponse(situations[0], *models.NewEmptyReferences(), api.Clock))
}

// findSituationAlert returns the first alert whose raw id or situation id is requested.
// agencyID is the fallback other endpoints use when the alert names no agency.
func findSituationAlert(alerts []gtfs.Alert, agencyID, requested string) (gtfs.Alert, bool) {
	for _, alert := range alerts {
		if alertMatchesSituationID(alert, agencyID, requested) {
			return alert, true
		}
	}
	return gtfs.Alert{}, false
}

// alertMatchesSituationID reports whether requested names this alert. An empty
// alert id never matches. The situation id uses the alert's own agency when an
// informed entity names one, and agencyID otherwise. That is the same fallback
// situationRefsFromAlerts uses when it publishes situationIds.
func alertMatchesSituationID(alert gtfs.Alert, agencyID, requested string) bool {
	if alert.ID == "" || requested == "" {
		return false
	}
	if alert.ID == requested {
		return true
	}
	return situationID(alert.ID, agencyIDForAlert(alert, agencyID)) == requested
}
