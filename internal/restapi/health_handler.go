package restapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/logging"
)

// DataFreshness represents the last update timestamps for GTFS data and
// which static bundle is loaded.
type DataFreshness struct {
	StaticGtfsLastUpdated *time.Time `json:"staticGtfsLastUpdated,omitempty"`
	// StaticGtfsHash is the hex sha256 of the raw static GTFS zip, so an
	// external service can tell whether it validates against the same bundle.
	StaticGtfsHash string               `json:"staticGtfsHash,omitempty"`
	RealtimeFeeds  map[string]time.Time `json:"realtimeFeeds,omitempty"`
}

// dataFreshness reports when the static and realtime data were last updated
// and which static bundle is loaded. A database with no import yet is not an
// error: the static fields are left empty. Any other query failure is
// returned, so callers don't report a missing bundle when the read failed.
func (api *RestAPI) dataFreshness(ctx context.Context) (DataFreshness, error) {
	freshness := DataFreshness{RealtimeFeeds: api.GtfsManager.GetFeedUpdateTimes()}

	metadata, err := api.GtfsManager.GtfsDB.Queries.GetImportMetadata(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return freshness, nil
	}
	if err != nil {
		return DataFreshness{}, err
	}

	if metadata.ImportTime != 0 {
		importTime := time.Unix(0, metadata.ImportTime).UTC()
		freshness.StaticGtfsLastUpdated = &importTime
	}
	freshness.StaticGtfsHash = metadata.FileHash
	return freshness, nil
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

	reqLogger := logging.ForComponent(r.Context(), "http_server")
	// 3. Connectivity Check: Is the database actually reachable?
	if err := api.GtfsManager.GtfsDB.DB.PingContext(r.Context()); err != nil {
		logging.LogError(reqLogger, "GTFS DB ping failed", err)
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
		f, err := api.dataFreshness(r.Context())
		if err != nil {
			logging.LogError(reqLogger, "reading data freshness failed", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(HealthResponse{
				Status: "unavailable",
				Detail: "reading data freshness failed",
			})
			return
		}
		freshness = &f
	}

	// All checks passed
	response := HealthResponse{
		Status:        "ok",
		DataFreshness: freshness,
	}

	expiresAt := api.GtfsManager.FeedExpiresAt(r.Context())
	if !expiresAt.IsZero() {
		response.FeedExpiresAt = expiresAt.Format(time.RFC3339)
		response.DataExpired = time.Now().After(expiresAt)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
