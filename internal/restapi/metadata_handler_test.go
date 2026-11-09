package restapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestMetadataHandler_NilManager(t *testing.T) {
	api := &RestAPI{
		Application: &app.Application{
			GtfsManager: nil,
		},
	}

	req, err := http.NewRequest("GET", "/api/v2/metadata.json", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(api.metadataHandler)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusServiceUnavailable {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusServiceUnavailable)
	}
}

func TestMetadataHandler(t *testing.T) {
	api := &RestAPI{
		Application: &app.Application{
			GtfsManager: newTestManagerNoData(t),
		},
	}

	// Manually set some dummy update times
	now := time.Now().UTC()
	api.GtfsManager.MarkReady() // Mock initialization

	// Set static last updated
	staticTime := now.Add(-1 * time.Hour)
	api.GtfsManager.SetStaticLastUpdatedForTest(context.Background(), staticTime)

	// Ensure the map is initialized since we mock the Manager
	api.GtfsManager.SetFeedUpdateTimeForTest("trip_updates", now)

	req, err := http.NewRequest("GET", "/api/v2/metadata.json", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(api.metadataHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response DataFreshness
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.RealtimeFeeds == nil {
		t.Errorf("Expected RealtimeFeeds map to be initialized")
	}

	if _, exists := response.RealtimeFeeds["trip_updates"]; !exists {
		t.Errorf("Expected trip_updates feed in response")
	}

	if response.StaticGtfsLastUpdated.Unix() != staticTime.Unix() {
		t.Errorf("Expected StaticGtfsLastUpdated to match set time")
	}
}

// rabaZipSha256 hashes the static fixture the way an external consumer such
// as Watchdog would, independently of what Maglev stored.
func rabaZipSha256(t *testing.T) string {
	t.Helper()
	zipBytes, err := os.ReadFile(filepath.Join("../../testdata", "raba.zip"))
	require.NoError(t, err)
	sum := sha256.Sum256(zipBytes)
	return hex.EncodeToString(sum[:])
}

func TestMetadataHandler_StaticGtfsHash(t *testing.T) {
	api := createTestApi(t)

	resp, body := callAPIHandler[map[string]any](t, api, "/api/v2/metadata.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, rabaZipSha256(t), body["staticGtfsHash"])
}

func TestMetadataHandler_NoImportOmitsHash(t *testing.T) {
	api := &RestAPI{
		Application: &app.Application{
			GtfsManager: newTestManagerNoData(t),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/metadata.json", nil)
	rr := httptest.NewRecorder()
	api.metadataHandler(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.NotContains(t, body, "staticGtfsHash")
}

// newTestManagerWithoutImportMetadata returns a manager whose database is
// reachable but fails import metadata reads, unlike an empty database.
func newTestManagerWithoutImportMetadata(t *testing.T) *gtfs.Manager {
	t.Helper()
	manager := newTestManagerNoData(t)
	_, err := manager.GtfsDB.DB.Exec("DROP TABLE import_metadata")
	require.NoError(t, err)
	return manager
}

func TestMetadataHandler_ImportMetadataQueryFails(t *testing.T) {
	manager := newTestManagerWithoutImportMetadata(t)
	api := &RestAPI{
		Application: &app.Application{
			GtfsManager: manager,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/metadata.json", nil)
	rr := httptest.NewRecorder()
	api.metadataHandler(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body["status"])
	assert.NotContains(t, body, "staticGtfsHash")
}
