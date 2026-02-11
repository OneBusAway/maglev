package restapi

import (
	"encoding/json"
	"net/http"

	"maglev.onebusaway.org/internal/logging"
)

// JSON response from the health endpoint.
type HealthResponse struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// verifies database connectivity.
func (api *RestAPI) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check database connectivity
	if api.Application == nil || api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.DB == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status: "unavailable",
			Detail: "database not initialized",
		})
		return
	}

	if err := api.GtfsManager.GtfsDB.DB.PingContext(r.Context()); err != nil {
		logging.LogError(api.Logger, "GTFS DB ping failed", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status: "unavailable",
			Detail: "database connection failed",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HealthResponse{
		Status: "ok",
	})
}
