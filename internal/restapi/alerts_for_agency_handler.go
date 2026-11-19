package restapi

import "net/http"

// alertsForAgencyHandler republishes the service alerts attributed to an
// agency as a GTFS-RT feed. The feed is serialized when the realtime
// snapshot is rebuilt, so a request only sends its bytes. An unknown agency
// gets a feed with no entities. routeFilterId and removeAgencyIds are not
// implemented.
func (api *RestAPI) alertsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, ok := api.gtfsRealtimeAgencyID(w, r)
	if !ok {
		return
	}

	body, err := api.GtfsManager.AlertFeedForAgency(agencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	writeGtfsRealtimeFeed(w, r, body)
}
