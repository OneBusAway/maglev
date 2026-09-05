package restapi

import (
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/models"
)

// currentTimeHandler returns the server's current time as a JSON response.
// readableTime is formatted in the primary agency's local timezone.
func (api *RestAPI) currentTimeHandler(w http.ResponseWriter, r *http.Request) {
	if !api.GtfsManager.IsReady() {
		http.Error(w, "Service Unavailable: GTFS data invalid", http.StatusServiceUnavailable)
		return
	}

	loc := agencyTimezone(api, r)
	timeData := models.NewCurrentTimeDataInLocation(api.Clock.Now(), loc)
	response := models.NewOKResponse(timeData, api.Clock)

	api.sendResponse(w, r, response)
}

// agencyTimezone returns the primary agency's IANA timezone location.
// Falls back to UTC if the timezone cannot be loaded.
func agencyTimezone(api *RestAPI, r *http.Request) *time.Location {
	agencies, err := api.GtfsManager.GetAgencies(r.Context())
	if err != nil || len(agencies) == 0 {
		return time.UTC
	}
	loc, err := loadAgencyLocation(agencies[0].ID, agencies[0].Timezone)
	if err != nil {
		api.Logger.Warn("failed to load agency timezone", "agencyID", agencies[0].ID, "error", err)
		return time.UTC
	}
	return loc
}
