package main

import (
	"encoding/json"
	"maglev.onebusaway.org/internal/models"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCurrentTimeHandler(t *testing.T) {
	// Create a new application instance with any configuration needed
	app := &application{
		config: config{
			env:     "test",
			apiKeys: []string{"testkey"},
		},
	}

	// Create a new HTTP request with the correct URL path
	req, err := http.NewRequest("GET", "/api/where/current-time.json?key=testkey", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()

	// Call the handler function directly, passing in the ResponseRecorder and Request
	app.currentTimeHandler(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Check the content type
	contentType := rr.Header().Get("Content-Type")
	expectedContentType := "application/json"
	if contentType != expectedContentType {
		t.Errorf("handler returned wrong content type: got %v want %v", contentType, expectedContentType)
	}

	// Decode the JSON response
	var response models.ResponseModel
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	if err != nil {
		t.Errorf("error decoding response: %v", err)
	}

	// Check basic response structure
	if response.Code != http.StatusOK {
		t.Errorf("wrong status code in response: got %v want %v", response.Code, http.StatusOK)
	}

	if response.Text != "OK" {
		t.Errorf("wrong text in response: got %v want %v", response.Text, "OK")
	}

	if response.Version != 2 {
		t.Errorf("wrong version in response: got %v want %v", response.Version, 2)
	}

	// Get the current time to compare with response time
	now := time.Now().UnixNano() / int64(time.Millisecond)

	// The response time should be within a reasonable range of the current time
	// Let's say 5 seconds (5000 milliseconds)
	if response.CurrentTime < now-5000 || response.CurrentTime > now+5000 {
		t.Errorf("response time is outside of reasonable range: got %v, current time: %v", response.CurrentTime, now)
	}

	// Test the data structure
	// First, we need to cast the interface{} to the expected type
	responseData, ok := response.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("could not cast data to expected type")
	}

	// Check that entry exists
	entry, ok := responseData["entry"].(map[string]interface{})
	if !ok {
		t.Fatalf("could not find entry in response data")
	}

	// Check that time and readableTime exist in entry
	_, ok = entry["time"].(float64)
	if !ok {
		t.Errorf("could not find time in entry")
	}

	_, ok = entry["readableTime"].(string)
	if !ok {
		t.Errorf("could not find readableTime in entry")
	}

	// Check that references exists and has the expected structure
	references, ok := responseData["references"].(map[string]interface{})
	if !ok {
		t.Fatalf("could not find references in response data")
	}

	// Check that all expected arrays exist in references
	referencesFields := []string{"agencies", "routes", "situations", "stopTimes", "stops", "trips"}
	for _, field := range referencesFields {
		array, ok := references[field].([]interface{})
		if !ok {
			t.Errorf("could not find %s array in references", field)
		} else if len(array) != 0 {
			t.Errorf("expected empty %s array, got length %d", field, len(array))
		}
	}
}

func TestCurrentTimeHandlerInvalidKey(t *testing.T) {
	// Create a new application instance
	app := &application{
		config: config{
			env:     "test",
			apiKeys: []string{"valid_key"},
		},
	}

	// Test with invalid key
	req, err := http.NewRequest("GET", "/api/where/current-time.json?key=invalid_key", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	app.currentTimeHandler(rr, req)

	// Check status code
	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code for invalid key: got %v want %v",
			status, http.StatusUnauthorized)
	}

	// Parse response
	var response struct {
		Code        int    `json:"code"`
		CurrentTime int64  `json:"currentTime"`
		Text        string `json:"text"`
		Version     int    `json:"version"`
	}

	err = json.Unmarshal(rr.Body.Bytes(), &response)
	if err != nil {
		t.Errorf("error parsing response: %v", err)
	}

	// Check response structure
	if response.Code != http.StatusUnauthorized {
		t.Errorf("expected code 401, got %d", response.Code)
	}

	if response.Text != "permission denied" {
		t.Errorf("expected text 'permission denied', got %s", response.Text)
	}

	if response.Version != 1 {
		t.Errorf("expected version 1, got %d", response.Version)
	}
}
