package restapi

import (
	"net/http"

	"github.com/OneBusAway/go-gtfs"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
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

// findSituationAlert returns the alert whose published situation id is
// requested, otherwise the first alert whose raw id is. Published ids are
// checked first so an earlier alert whose raw id equals another alert's
// published id does not hide it. agencyID is the fallback other endpoints
// use when the alert names no agency.
func findSituationAlert(alerts []gtfs.Alert, agencyID, requested string) (gtfs.Alert, bool) {
	if requested == "" {
		return gtfs.Alert{}, false
	}
	var rawMatch gtfs.Alert
	foundRaw := false
	for _, alert := range alerts {
		if alert.ID == "" {
			continue
		}
		if internalgtfs.SituationID(alert.ID, agencyIDForAlert(alert, agencyID)) == requested {
			return alert, true
		}
		if !foundRaw && alert.ID == requested {
			rawMatch = alert
			foundRaw = true
		}
	}
	if foundRaw {
		return rawMatch, true
	}
	return gtfs.Alert{}, false
}
