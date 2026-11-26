package restapi

import (
	"net/http"
	"strings"

	"maglev.onebusaway.org/internal/servicedate"
	"maglev.onebusaway.org/internal/utils"
)

// etagSchedule is etagStatic for the schedule endpoints. Their validator also
// carries the agency-local service day when date is omitted, so a conditional
// request made after the day rolls over gets the new day's schedule, not a 304.
func etagSchedule(api *RestAPI, handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	wrapped := ETagMiddleware(api.scheduleETag)(http.HandlerFunc(handler))
	return func(w http.ResponseWriter, r *http.Request) {
		wrapped.ServeHTTP(w, r)
	}
}

// scheduleETag returns the validator for a schedule request. An explicit date does
// not depend on the clock, so it keeps the static feed's ETag. An omitted date
// resolves to today's service day in the agency's timezone, so that day is part
// of the validator.
func (api *RestAPI) scheduleETag(r *http.Request) string {
	staticETag := api.GtfsManager.GetSystemETag(r.Context())
	if staticETag == "" || r.URL.Query().Get("date") != "" {
		return staticETag
	}
	serviceDay, err := api.currentServiceDay(r)
	if err != nil {
		return staticETag
	}
	// The static ETag is a quoted string, so the day goes inside the quotes.
	return strings.TrimSuffix(staticETag, `"`) + "-" + serviceDay + `"`
}

// currentServiceDay returns today's service day as YYYYMMDD, in the timezone of
// the agency named by the request's ID.
func (api *RestAPI) currentServiceDay(r *http.Request) (string, error) {
	agencyID, err := utils.ExtractAgencyID(utils.ExtractIDFromParams(r))
	if err != nil {
		return "", err
	}
	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(r.Context(), agencyID)
	if err != nil {
		return "", err
	}
	loc, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		return "", err
	}
	return servicedate.FromInstant(api.Clock.Now(), loc).String(), nil
}
