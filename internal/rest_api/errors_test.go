package restapi

import (
	"encoding/json"
	"errors"
	"maglev.onebusaway.org/internal/app"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerErrorResponse(t *testing.T) {
	// Create a mock Application
	app := &app.Application{
		// If you have a logger in your app struct, you can mock it here
		// errorLog: log.New(ioutil.Discard, "", 0), // Silent logger
	}

	api := &RestAPI{Application: app}

	// Create a mock request and response recorder
	r, err := http.NewRequest("GET", "/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()

	// Create a test error
	testErr := errors.New("test server error")

	// Call serverErrorResponse
	api.serverErrorResponse(rr, r, testErr)

	// Check the status code
	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusInternalServerError)
	}

	// Check the content type
	contentType := rr.Header().Get("Content-Type")
	expectedContentType := "application/json"
	if contentType != expectedContentType {
		t.Errorf("handler returned wrong content type: got %v want %v",
			contentType, expectedContentType)
	}

	// Parse the response body
	var response struct {
		Code        int    `json:"code"`
		CurrentTime int64  `json:"currentTime"`
		Text        string `json:"text"`
		Version     int    `json:"version"`
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Errorf("error parsing response: %v", err)
	}

	// Check response values
	if response.Code != http.StatusInternalServerError {
		t.Errorf("unexpected code in response: got %d want %d",
			response.Code, http.StatusInternalServerError)
	}

	if response.Text != "internal server error" {
		t.Errorf("unexpected text in response: got %s want %s",
			response.Text, "internal server error")
	}

	if response.Version != 1 {
		t.Errorf("unexpected version in response: got %d want %d",
			response.Version, 1)
	}

	// Check that the timestamp is reasonable
	now := time.Now().UnixNano() / int64(time.Millisecond)
	if response.CurrentTime < now-5000 || response.CurrentTime > now+5000 {
		t.Errorf("timestamp out of reasonable range: got %d, current time: %d",
			response.CurrentTime, now)
	}
}
