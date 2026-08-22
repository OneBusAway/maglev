package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// metricsHandler returns aggregate agency coverage, scheduled trip counts,
// and GTFS-RT matching health, keyed by agency ID.
func (api *RestAPI) metricsHandler(w http.ResponseWriter, r *http.Request) {
	snapshot, err := api.GtfsManager.GetMetrics(r.Context(), api.Clock.Now())
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	metricsEntry := models.MetricsModel{
		AgenciesWithCoverageCount:   len(snapshot.AgencyIDs),
		AgencyIDs:                   snapshot.AgencyIDs,
		ScheduledTripsCount:         snapshot.ScheduledTripsCount,
		RealtimeRecordsTotal:        snapshot.RealtimeRecordsTotal,
		RealtimeTripCountsMatched:   snapshot.RealtimeTripCountsMatched,
		RealtimeTripCountsUnmatched: snapshot.RealtimeTripCountsUnmatched,
		RealtimeTripIDsUnmatched:    snapshot.RealtimeTripIDsUnmatched,
		StopIDsMatchedCount:         snapshot.StopIDsMatchedCount,
		StopIDsUnmatchedCount:       snapshot.StopIDsUnmatchedCount,
		StopIDsUnmatched:            snapshot.StopIDsUnmatched,
		TimeSinceLastRealtimeUpdate: snapshot.TimeSinceLastRealtimeUpdate,
	}

	response := models.NewEntryResponse(
		metricsEntry,
		*models.NewEmptyReferences(),
		api.Clock,
	)

	api.sendResponse(w, r, response)
}
