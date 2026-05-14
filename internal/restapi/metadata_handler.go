package restapi

import (
	"encoding/json"
	"net/http"
	"time"
)

// metadataHandler returns system metadata including data freshness indicators.
func (api *RestAPI) metadataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if api.GtfsManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "unavailable",
			"detail": "GTFS Manager not initialized",
		})
		return
	}

	t := api.GtfsManager.GetStaticLastUpdated(r.Context())
	var staticTime *time.Time
	if !t.IsZero() {
		staticTime = &t
	}

	response := DataFreshness{
		StaticGtfsLastUpdated: staticTime,
		RealtimeFeeds:         api.GtfsManager.GetFeedUpdateTimes(),
	}

	// Ensure the map isn't nil for JSON serialization
	if response.RealtimeFeeds == nil {
		response.RealtimeFeeds = make(map[string]time.Time)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
