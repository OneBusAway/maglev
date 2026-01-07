package restapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/clock"
)

func TestCurrentTimeHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/current-time.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestCurrentTimeHandler(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/current-time.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check the content type
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/json")

	// Check basic response structure
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)

	// Get the current time to compare with response time
	now := time.Now().UnixNano() / int64(time.Millisecond)

	// The response time should be within a reasonable range of the current time
	// Let's say 5 seconds (5000 milliseconds)
	assert.False(t, model.CurrentTime < now-5000 || model.CurrentTime > now+5000)

	// Test the data structure
	// First, we need to cast the interface{} to the expected type
	responseData, ok := model.Data.(map[string]interface{})
	assert.True(t, ok, "could not cast data to expected type")

	// Check that entry exists
	entry, ok := responseData["entry"].(map[string]interface{})
	assert.True(t, ok, "could not find entry in response data")

	// Check that time and readableTime exist in entry
	_, ok = entry["time"].(float64)
	assert.True(t, ok, "could not find time in entry")

	_, ok = entry["readableTime"].(string)
	assert.True(t, ok, "could not find readableTime in entry")

	// Check that references exist and have the expected structure
	references, ok := responseData["references"].(map[string]interface{})
	assert.True(t, ok, "could not find references in response data")

	// Check that all expected arrays exist in references
	referencesFields := []string{"agencies", "routes", "situations", "stopTimes", "stops", "trips"}
	for _, field := range referencesFields {
		array, ok := references[field].([]interface{})
		assert.True(t, ok, "could not find %s array in references", field)
		assert.Equal(t, 0, len(array), "expected empty %s array, got length %d", field, len(array))
	}
}

// TestCurrentTimeHandler_DeterministicTime tests the current-time endpoint with a mock clock
// to verify that the response contains the exact time from the clock.
func TestCurrentTimeHandler_DeterministicTime(t *testing.T) {
	// Create a fixed time: June 15, 2024 at 2:30 PM UTC
	fixedTime := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	// Create API with mock clock
	api := createTestApiWithClock(t, mockClock)
	_, response := serveApiAndRetrieveEndpoint(t, api, "/api/where/current-time.json?key=TEST")

	// Response time should be exactly the fixed time
	expectedMs := fixedTime.UnixMilli()
	assert.Equal(t, expectedMs, response.CurrentTime, "Response currentTime should equal mock clock time")

	// Entry time should also match
	responseData := response.Data.(map[string]interface{})
	entry := responseData["entry"].(map[string]interface{})
	assert.Equal(t, float64(expectedMs), entry["time"], "Entry time should equal mock clock time")

	// Readable time should match
	expectedReadable := fixedTime.Format(time.RFC3339)
	assert.Equal(t, expectedReadable, entry["readableTime"], "Readable time should match mock clock")
}
