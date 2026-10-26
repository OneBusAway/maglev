package restapi

import (
	"encoding/json"
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/logging"
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

	response, err := api.dataFreshness(r.Context())
	if err != nil {
		logging.LogError(logging.ForComponent(r.Context(), "http_server"), "reading data freshness failed", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "unavailable",
			"detail": "reading data freshness failed",
		})
		return
	}

	// Ensure the map isn't nil for JSON serialization
	if response.RealtimeFeeds == nil {
		response.RealtimeFeeds = make(map[string]time.Time)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
