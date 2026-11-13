package restapi

import (
	"log/slog"
	"net/http"
	"strings"

	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/utils"
)

// gtfsRealtimeExtension is the only format the GTFS-RT exports serve.
// Java's .pbtext text format has no known caller.
const gtfsRealtimeExtension = ".pb"

// vehiclePositionsForAgencyHandler republishes the realtime vehicles
// attributed to an agency as a GTFS-RT feed. An unknown agency gets a feed
// with no entities. routeFilterId and removeAgencyIds are not implemented.
func (api *RestAPI) vehiclePositionsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, ok := api.gtfsRealtimeAgencyID(w, r)
	if !ok {
		return
	}

	positions := api.GtfsManager.VehiclePositionsForAgency(agencyID)
	body, err := encodeGtfsRealtimeFeed(newVehiclePositionsFeed(positions))
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	writeGtfsRealtimeFeed(w, r, body)
}

// gtfsRealtimeAgencyID reads the agency ID of a GTFS-RT export request and
// checks its suffix and time parameter. On failure it has already written
// the error response, and ok is false.
func (api *RestAPI) gtfsRealtimeAgencyID(w http.ResponseWriter, r *http.Request) (agencyID string, ok bool) {
	agencyID, isProtobuf := strings.CutSuffix(r.PathValue("id"), gtfsRealtimeExtension)
	if !isProtobuf {
		api.sendNotFound(w, r)
		return "", false
	}
	if err := utils.ValidateID(agencyID); err != nil {
		api.validationErrorResponse(w, r, map[string][]string{"id": {err.Error()}})
		return "", false
	}
	if err := utils.ValidateGtfsRealtimeTime(r.URL.Query().Get("time")); err != nil {
		api.validationErrorResponse(w, r, map[string][]string{"time": {err.Error()}})
		return "", false
	}
	return agencyID, true
}

// writeGtfsRealtimeFeed sends a serialized feed with its ETag, or 304 when
// the client already has it.
func writeGtfsRealtimeFeed(w http.ResponseWriter, r *http.Request, body []byte) {
	etag := weakETag(body)
	w.Header().Set("ETag", etag)
	if ifNoneMatchHits(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", gtfsRealtimeContentType)
	w.WriteHeader(http.StatusOK)
	// The 200 is already sent, so a failed write can only be logged.
	if _, err := w.Write(body); err != nil {
		logging.LogError(logging.ForComponent(r.Context(), "http_server"), "write GTFS-RT feed", err,
			slog.String("path", r.URL.Path))
	}
}
