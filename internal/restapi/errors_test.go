package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

func TestServerErrorResponse(t *testing.T) {
	// Create a mock Application with Clock
	application := &app.Application{
		Clock: clock.RealClock{},
	}

	api := &RestAPI{Application: application}

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

	if response.Version != models.APIVersion {
		t.Errorf("unexpected version in response: got %d want %d",
			response.Version, models.APIVersion)
	}

	// Check that the timestamp is reasonable
	now := time.Now().UnixNano() / int64(time.Millisecond)
	if response.CurrentTime < now-5000 || response.CurrentTime > now+5000 {
		t.Errorf("timestamp out of reasonable range: got %d, current time: %d",
			response.CurrentTime, now)
	}
}

func TestCanceledOrServerErrorResponse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	newRequest := func(ctx context.Context) (*httptest.ResponseRecorder, *http.Request) {
		req := httptest.NewRequest(http.MethodGet, "/api/where/arrivals-and-departures-for-location.json", nil).WithContext(ctx)
		return httptest.NewRecorder(), req
	}

	t.Run("client cancellation writes nothing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec, req := newRequest(ctx)

		api.canceledOrServerErrorResponse(rec, req, ctx, errors.New("boom"))

		assert.Empty(t, rec.Body.String(), "no body should be written when the client cancels")
	})

	t.Run("expired deadline writes gateway timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		rec, req := newRequest(ctx)

		api.canceledOrServerErrorResponse(rec, req, ctx, errors.New("boom"))

		assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
		assert.Contains(t, rec.Body.String(), "gateway timeout")
	})

	t.Run("genuine failure writes server error", func(t *testing.T) {
		ctx := context.Background()
		rec, req := newRequest(ctx)

		api.canceledOrServerErrorResponse(rec, req, ctx, errors.New("boom"))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
