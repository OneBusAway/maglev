package restapi

import (
	"encoding/json"
	"net/http"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
)

func (api *RestAPI) handleAlerts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	routeID := query.Get("routeId")
	stopID := query.Get("stopId")
	agencyID := query.Get("agencyId")

	var alerts []gtfs.Alert

	if routeID != "" {
		alerts = api.GtfsManager.GetAlertsForRoute(routeID)
	} else if stopID != "" {
		alerts = api.GtfsManager.GetAlertsForStop(stopID)
	} else {
		alerts = api.GtfsManager.GetAllAlerts()
	}

	if agencyID != "" {
		filtered := make([]gtfs.Alert, 0)
		for _, alert := range alerts {
			for _, entity := range alert.InformedEntities {
				if entity.AgencyID != nil && *entity.AgencyID == agencyID {
					filtered = append(filtered, alert)
					break
				}
			}
		}
		alerts = filtered
	}

	situations := api.BuildSituationReferences(alerts, agencyID)

	references := models.NewEmptyReferences()

	resp := models.NewEntryResponse(situations, references, api.Clock)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}
