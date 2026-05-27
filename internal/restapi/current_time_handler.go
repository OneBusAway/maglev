package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// currentTimeHandler returns the server's current time as a JSON response.
func (api *RestAPI) currentTimeHandler(w http.ResponseWriter, r *http.Request) {
	// Health Check: fail if GTFS data is invalid
	if !api.GtfsManager.IsReady() {
		http.Error(w, "Service Unavailable: GTFS data invalid", http.StatusServiceUnavailable)
		return
	}

	timeData := models.NewCurrentTimeData(api.Clock.Now())
	response := models.NewOKResponse(timeData, api.Clock)

	api.sendResponse(w, r, response)
}
