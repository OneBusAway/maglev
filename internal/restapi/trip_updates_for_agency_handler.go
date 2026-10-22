package restapi

import (
	"net/http"

	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

// tripUpdatesForAgencyHandler republishes the ingested trip updates
// attributed to an agency as a GTFS-RT feed. Each snapshot's feed is
// serialized on its first request and shared by later ones. An unknown
// agency gets a feed with no entities. routeFilterId and removeAgencyIds
// are not implemented.
func (api *RestAPI) tripUpdatesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, ok := api.gtfsRealtimeAgencyID(w, r)
	if !ok {
		return
	}

	updates := api.GtfsManager.TripUpdatesForAgency(agencyID)
	body, err := updates.Encoded(encodeTripUpdatesFeed)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	writeGtfsRealtimeFeed(w, r, body)
}

func encodeTripUpdatesFeed(updates internalgtfs.AgencyTripUpdates) ([]byte, error) {
	return encodeGtfsRealtimeFeed(newTripUpdatesFeed(updates))
}
