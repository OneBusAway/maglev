package restapi

import (
	"fmt"
	"net/http"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
)

// situationHandler serves a single GTFS-RT service alert (OneBusAway "Situation")
// by its alert id.
func (api *RestAPI) situationHandler(w http.ResponseWriter, r *http.Request) {
	situationID, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	var alert gtfs.Alert
	found := false
	for _, candidate := range api.GtfsManager.GetAllAlerts() {
		if candidate.ID == situationID {
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
	if len(situations) == 0 {
		api.serverErrorResponse(w, r, fmt.Errorf("unexpected empty situation build for id %q", situationID))
		return
	}

	references := models.NewEmptyReferences()
	response := models.NewEntryResponse(situations[0], *references, api.Clock)
	api.sendResponse(w, r, response)
}
