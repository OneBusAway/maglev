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
		http.Error(w, "GTFS Manager not initialized", http.StatusServiceUnavailable)
		return
	}

	response := DataFreshness{
		StaticGtfsLastUpdated: api.GtfsManager.GetStaticLastUpdated(),
		RealtimeFeeds:         api.GtfsManager.GetFeedUpdateTimes(),
	}

	// Ensure the map isn't nil for JSON serialization
	if response.RealtimeFeeds == nil {
		response.RealtimeFeeds = make(map[string]time.Time)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
