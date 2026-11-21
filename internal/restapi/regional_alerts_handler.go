package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/utils"
)

// regionalAlertsHandler serves GET /api/gtfs_realtime/alerts.pb, returning a
// single GTFS-RT FeedMessage containing all service alerts across the region
// as proposed in ADR-0003. The feed is serialized at snapshot rebuild, so a
// request only sends its bytes.
func (api *RestAPI) regionalAlertsHandler(w http.ResponseWriter, r *http.Request) {
	if err := utils.ValidateGtfsRealtimeTime(r.URL.Query().Get("time")); err != nil {
		api.validationErrorResponse(w, r, map[string][]string{"time": {err.Error()}})
		return
	}

	body, err := api.GtfsManager.RegionalAlertFeed()
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	writeGtfsRealtimeFeed(w, r, body)
}
