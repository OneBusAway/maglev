package restapi

import (
	"context"
	"net/http"

	"maglev.onebusaway.org/internal/utils"
)

// extractAndValidateID extracts the ID from the URL, validates it, and handles errors
func (api *RestAPI) extractAndValidateID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := utils.ExtractIDFromParams(r)

	if err := utils.ValidateID(id); err != nil {
		fieldErrors := map[string][]string{"id": {err.Error()}}
		api.validationErrorResponse(w, r, fieldErrors)
		return "", false
	}

	return id, true
}

// extractAndValidateAgencyCodeID extracts and validates a combined agency_code ID from the URL
func (api *RestAPI) extractAndValidateAgencyCodeID(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return "", "", false
	}

	agencyID, codeID, err := utils.ExtractAgencyIDAndCodeID(id)
	if err != nil {
		fieldErrors := map[string][]string{"id": {err.Error()}}
		api.validationErrorResponse(w, r, fieldErrors)
		return "", "", false
	}

	return agencyID, codeID, true
}

// stopAgencyIDs maps raw stop IDs to the agency in each stop's public ID. Java
// OneBusAway gives each stop one ID, under its feed's first agency, whichever
// agencies' routes serve it.
type stopAgencyIDs map[string]string

// combinedStopID returns the stop's public ID, or "" for a stop with no agency.
func (s stopAgencyIDs) combinedStopID(stopID string) string {
	return utils.FormCombinedID(s[stopID], stopID)
}

// combinedStopIDs returns the public IDs of stopIDs, in order, leaving out
// stops with no agency.
func (s stopAgencyIDs) combinedStopIDs(stopIDs []string) []string {
	combined := make([]string, 0, len(stopIDs))
	for _, stopID := range stopIDs {
		if id := s.combinedStopID(stopID); id != "" {
			combined = append(combined, id)
		}
	}
	return combined
}

// feedAgency is the feed's default agency. Java names service IDs after it,
// whichever agency runs the trip.
type feedAgency string

// feedAgency returns the agency service IDs are named after.
func (api *RestAPI) feedAgency() feedAgency {
	return feedAgency(api.GtfsManager.FeedAgencyID())
}

// combinedServiceID returns the service's public ID.
func (a feedAgency) combinedServiceID(serviceID string) string {
	return utils.FormCombinedID(string(a), serviceID)
}

// loadStopAgencyIDs reads the agency in each stop's public ID. It is the one way to
// learn a stop's agency; the routes serving a stop don't decide it.
func (api *RestAPI) loadStopAgencyIDs(ctx context.Context, stopIDs []string) (stopAgencyIDs, error) {
	rows, err := utils.QueryInBatches(ctx, stopIDs, api.GtfsManager.GtfsDB.Queries.GetStopAgencyIDs)
	if err != nil {
		return nil, err
	}
	agencyIDs := make(stopAgencyIDs, len(rows))
	for _, row := range rows {
		agencyIDs[row.StopID] = row.AgencyID
	}
	return agencyIDs, nil
}

// requireStopAgency sends not found unless agencyID is the agency in the stop's ID,
// and reports whether the request may go on. The stop doesn't exist under any other
// prefix, even an agency whose routes serve it.
func (api *RestAPI) requireStopAgency(w http.ResponseWriter, r *http.Request, agencyID, stopID string) bool {
	agencyIDs, err := api.loadStopAgencyIDs(r.Context(), []string{stopID})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return false
	}
	if agencyIDs[stopID] != agencyID {
		api.sendNotFound(w, r)
		return false
	}
	return true
}
