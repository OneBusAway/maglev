package restapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

// gtfsRealtimeFormat is the serialization an export path's suffix selects.
type gtfsRealtimeFormat int

const (
	gtfsRealtimeBinary gtfsRealtimeFormat = iota
	gtfsRealtimeText
)

const (
	gtfsRealtimeBinarySuffix = ".pb"
	gtfsRealtimeTextSuffix   = ".pbtext"
	removeAgencyIDsParam     = "removeAgencyIds"
	routeFilterIDParam       = "routeFilterId"
)

// gtfsRealtimeExportRequest holds the parameters shared by every agency-wide
// GTFS-RT export.
type gtfsRealtimeExportRequest struct {
	AgencyID        string
	Format          gtfsRealtimeFormat
	Time            time.Time
	RouteFilterID   string
	RemoveAgencyIDs bool
}

// parseGtfsRealtimeAgencyPath splits an "{agencyID}.pb" or "{agencyID}.pbtext"
// path segment. The agency is a plain ID, not a combined entity ID.
func parseGtfsRealtimeAgencyPath(pathID string) (string, gtfsRealtimeFormat, bool) {
	if agencyID, ok := strings.CutSuffix(pathID, gtfsRealtimeTextSuffix); ok {
		return agencyID, gtfsRealtimeText, true
	}
	if agencyID, ok := strings.CutSuffix(pathID, gtfsRealtimeBinarySuffix); ok {
		return agencyID, gtfsRealtimeBinary, true
	}
	return "", gtfsRealtimeBinary, false
}

// gtfsRealtimeExportRequestOrRespond parses an export request and, when that
// fails, writes the 404, 400 or 500 response itself.
func (api *RestAPI) gtfsRealtimeExportRequestOrRespond(w http.ResponseWriter, r *http.Request) (gtfsRealtimeExportRequest, bool) {
	agencyID, format, ok := parseGtfsRealtimeAgencyPath(r.PathValue("id"))
	if !ok {
		api.sendNotFound(w, r)
		return gtfsRealtimeExportRequest{}, false
	}
	if err := utils.ValidateID(agencyID); err != nil {
		api.validationErrorResponse(w, r, map[string][]string{"id": {err.Error()}})
		return gtfsRealtimeExportRequest{}, false
	}
	req, fieldErrors, err := api.parseGtfsRealtimeExportRequest(r, agencyID, format)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return gtfsRealtimeExportRequest{}, false
	}
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return gtfsRealtimeExportRequest{}, false
	}
	return req, true
}

// parseGtfsRealtimeExportRequest reads the shared export query parameters.
// Field errors describe client mistakes; the error return is a lookup failure.
func (api *RestAPI) parseGtfsRealtimeExportRequest(r *http.Request, agencyID string, format gtfsRealtimeFormat) (gtfsRealtimeExportRequest, map[string][]string, error) {
	location, err := api.gtfsRealtimeRequestLocation(r.Context(), agencyID)
	if err != nil {
		return gtfsRealtimeExportRequest{}, nil, err
	}

	query := r.URL.Query()
	fieldErrors := make(map[string][]string)
	requestTime := parseGtfsRealtimeTime(query.Get("time"), location, api.Clock, fieldErrors)
	removeAgencyIDs, fieldErrors := parseRemoveAgencyIDs(query, fieldErrors)

	return gtfsRealtimeExportRequest{
		AgencyID:        agencyID,
		Format:          format,
		Time:            requestTime,
		RouteFilterID:   query.Get(routeFilterIDParam),
		RemoveAgencyIDs: removeAgencyIDs,
	}, fieldErrors, nil
}

// gtfsRealtimeRequestLocation resolves the zone date strings are read in:
// the agency's timezone, or UTC for an agency Maglev does not know.
func (api *RestAPI) gtfsRealtimeRequestLocation(ctx context.Context, agencyID string) (*time.Location, error) {
	agency, err := api.GtfsManager.FindAgency(ctx, agencyID)
	if err != nil {
		return nil, err
	}
	if agency == nil {
		return time.UTC, nil
	}
	return loadAgencyLocation(agency.ID, agency.Timezone)
}

// parseGtfsRealtimeTime applies Maglev's shared time syntax, then rejects
// pre-epoch instants because a feed header timestamp is unsigned.
func parseGtfsRealtimeTime(timeParam string, location *time.Location, c clock.Clock, fieldErrors map[string][]string) time.Time {
	_, requestTime, timeErrors, ok := utils.ParseTimeParameter(timeParam, location, c)
	if !ok {
		for field, messages := range timeErrors {
			fieldErrors[field] = append(fieldErrors[field], messages...)
		}
		return time.Time{}
	}
	if requestTime.Unix() < 0 {
		fieldErrors["time"] = append(fieldErrors["time"], "must not be before the Unix epoch")
	}
	return requestTime
}

// parseRemoveAgencyIDs defaults an absent or empty value to true. The shared
// ParseBoolParam reads a present empty value as false, so it only handles
// nonempty input here.
func parseRemoveAgencyIDs(query url.Values, fieldErrors map[string][]string) (bool, map[string][]string) {
	if query.Get(removeAgencyIDsParam) == "" {
		return true, fieldErrors
	}
	return utils.ParseBoolParam(query, removeAgencyIDsParam, true, fieldErrors)
}
