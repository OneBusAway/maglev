package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
)

func TestHandleAlerts(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC))

	testApp := &app.Application{
		GtfsManager: &gtfs.Manager{},
		Clock:       mockClock,
	}

	api := &RestAPI{
		Application: testApp,
	}

	req := httptest.NewRequest("GET", "/api/where/alerts.json?key=TEST", nil)
	w := httptest.NewRecorder()

	api.handleAlerts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", w.Code)
	}

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}

	var resp models.ResponseModel
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Code != 200 {
		t.Errorf("Expected response code 200, got %d", resp.Code)
	}

	t.Log("TestHandleAlerts passed successfully!")
}
