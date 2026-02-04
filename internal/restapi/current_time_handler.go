package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// Declare a handler which writes a JSON response with information about the
// current time.
func (api *RestAPI) currentTimeHandler(w http.ResponseWriter, r *http.Request) {
	// Health Check: fail if GTFS data is invalid
	if !api.GtfsManager.IsHealthy() {
		http.Error(w, "Service Unavailable: GTFS data invalid", http.StatusServiceUnavailable)
		return
	}

	timeData := models.NewCurrentTimeData(api.Clock.Now())
	response := models.NewOKResponse(timeData, api.Clock)

	api.sendResponse(w, r, response)
}
