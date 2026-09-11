package restapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
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
	assert.Equal(t, models.APIVersion, model.Version)

	// Get the current time to compare with response time
	now := time.Now().UnixNano() / int64(time.Millisecond)

	// The response time should be within a reasonable range of the current time
	// Let's say 5 seconds (5000 milliseconds)
	assert.False(t, model.CurrentTime < now-5000 || model.CurrentTime > now+5000)

	// Test the data structure
	// First, we need to cast the interface{} to the expected type
	responseData, ok := model.Data.(map[string]any)
	assert.True(t, ok, "could not cast data to expected type")

	// Check that entry exists
	entry, ok := responseData["entry"].(map[string]any)
	assert.True(t, ok, "could not find entry in response data")

	// Check that time and readableTime exist in entry
	_, ok = entry["time"].(float64)
	assert.True(t, ok, "could not find time in entry")

	_, ok = entry["readableTime"].(string)
	assert.True(t, ok, "could not find readableTime in entry")

	// Check that references exist and have the expected structure
	references, ok := responseData["references"].(map[string]any)
	assert.True(t, ok, "could not find references in response data")

	// Check that all expected arrays exist in references
	referencesFields := []string{"agencies", "routes", "situations", "stopTimes", "stops", "trips"}
	for _, field := range referencesFields {
		array, ok := references[field].([]any)
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
	responseData := response.Data.(map[string]any)
	entry := responseData["entry"].(map[string]any)
	assert.Equal(t, float64(expectedMs), entry["time"], "Entry time should equal mock clock time")

	// readableTime must be formatted in the agency's local timezone (America/Los_Angeles).
	agencyLoc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	expectedReadable := fixedTime.In(agencyLoc).Format(time.RFC3339)
	assert.Equal(t, expectedReadable, entry["readableTime"], "readableTime should use agency timezone")
}

func TestAgencyTimezone_EmptyAgencies(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.MarkReady()

	application := &app.Application{
		GtfsManager: manager,
		Clock:       clock.RealClock{},
	}
	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	loc := agencyTimezone(api, req)
	assert.Equal(t, time.UTC, loc)
}

func TestAgencyTimezone_InvalidTimezone(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.MarkReady()

	_, err := manager.GtfsDB.DB.ExecContext(context.Background(),
		`INSERT INTO agencies (id, name, url, timezone) VALUES (?, ?, ?, ?)`,
		"test-agency", "Test Agency", "http://example.com", "Not/AReal_Timezone",
	)
	require.NoError(t, err)

	application := &app.Application{
		GtfsManager: manager,
		Clock:       clock.RealClock{},
	}
	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	loc := agencyTimezone(api, req)
	assert.Equal(t, time.UTC, loc)
}

func TestAgencyTimezone_DBError(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.MarkReady()
	require.NoError(t, manager.GtfsDB.Close())

	application := &app.Application{
		GtfsManager: manager,
		Clock:       clock.RealClock{},
	}
	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	loc := agencyTimezone(api, req)
	assert.Equal(t, time.UTC, loc)
}
