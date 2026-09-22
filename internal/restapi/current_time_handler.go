package restapi

import (
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
)

// currentTimeHandler returns the server's current time as a JSON response.
// readableTime is formatted in the primary agency's local timezone.
// Readiness is owned by /healthz; this endpoint always returns the OBA JSON envelope.
func (api *RestAPI) currentTimeHandler(w http.ResponseWriter, r *http.Request) {
	loc := agencyTimezone(api, r)
	now := api.Clock.Now()
	timeData := models.NewCurrentTimeDataInLocation(now, loc)
	api.sendResponse(w, r, models.NewOKResponseAt(timeData, now))
}

// agencyTimezone returns the primary agency's IANA timezone location.
// Falls back to UTC if the timezone cannot be loaded.
func agencyTimezone(api *RestAPI, r *http.Request) *time.Location {
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		return time.UTC
	}
	reqLogger := logging.ForComponent(r.Context(), "http_server")
	agencies, err := api.GtfsManager.GetAgencies(r.Context())
	if err != nil || len(agencies) == 0 {
		return time.UTC
	}
	loc, err := loadAgencyLocation(agencies[0].ID, agencies[0].Timezone)
	if err != nil {
		reqLogger.Warn("failed to load agency timezone", "agencyID", agencies[0].ID, "error", err)
		return time.UTC
	}
	return loc
}
