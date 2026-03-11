package restapi

import (
	"encoding/json"
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/logging"
)

// DataFreshness represents the last update timestamps for GTFS data.
type DataFreshness struct {
	StaticGtfsLastUpdated *time.Time           `json:"staticGtfsLastUpdated,omitempty"`
	RealtimeFeeds         map[string]time.Time `json:"realtimeFeeds,omitempty"`
}

// HealthResponse represents the JSON response from the health endpoint.
type HealthResponse struct {
	Status        string         `json:"status"`
	Detail        string         `json:"detail,omitempty"`
	FeedExpiresAt string         `json:"feed_expires_at,omitempty"`
	DataExpired   bool           `json:"data_expired,omitempty"`
	DataFreshness *DataFreshness `json:"dataFreshness,omitempty"`
}

// healthHandler verifies database connectivity and readiness.
// It returns 503 Service Unavailable if the manager is not fully initialized and indexed.
func (api *RestAPI) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Liveness Check: Is the basic infrastructure initialized?
	if api.Application == nil || api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.DB == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status: "unavailable",
			Detail: "manager or database not initialized",
		})
		return
	}

	// 2. Readiness Check: Is the GTFS data indexed and ready for traffic?
	// This prevents routing traffic to "cold" instances still building spatial indexes.
	if !api.GtfsManager.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status: "starting",
			Detail: "GTFS data is being indexed and initialized",
		})
		return
	}

	// 3. Connectivity Check: Is the database actually reachable?
	if err := api.GtfsManager.GtfsDB.DB.PingContext(r.Context()); err != nil {
		logging.LogError(api.Logger, "GTFS DB ping failed", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status: "unavailable",
			Detail: "database connection failed",
		})
		return
	}

	// Fetch data freshness from the manager only if verbose=true is passed
	var freshness *DataFreshness
	if r.URL.Query().Get("verbose") == "true" {
		t := api.GtfsManager.GetStaticLastUpdated()
		var staticTime *time.Time
		if !t.IsZero() {
			staticTime = &t
		}
		freshness = &DataFreshness{
			StaticGtfsLastUpdated: staticTime,
			RealtimeFeeds:         api.GtfsManager.GetFeedUpdateTimes(),
		}
	}

	// All checks passed
	response := HealthResponse{
		Status:        "ok",
		DataFreshness: freshness,
	}

	expiresAt := api.GtfsManager.FeedExpiresAt()
	if !expiresAt.IsZero() {
		response.FeedExpiresAt = expiresAt.Format(time.RFC3339)
		response.DataExpired = time.Now().After(expiresAt)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
