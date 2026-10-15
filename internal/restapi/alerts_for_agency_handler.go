package restapi

import "net/http"

// alertsForAgencyHandler serves the alerts affecting an agency as a GTFS-RT
// feed built from Maglev's retained alert state.
func (api *RestAPI) alertsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	req, ok := api.gtfsRealtimeExportRequestOrRespond(w, r)
	if !ok {
		return
	}
	alerts := selectExportAlerts(api.GtfsManager.ExportAlerts(), req)
	api.writeGtfsRealtimeFeed(w, r, req.Format, buildAlertsFeed(alerts, req))
}
