package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/app"
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
	api.GtfsManager.SetStaticLastUpdatedForTest(staticTime)

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
