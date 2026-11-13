package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
)

// Unknown API paths, such as a GTFS-RT export this server doesn't serve, must 404
// with the JSON envelope rather than fall through to the web UI's landing page.
// The API key check runs first, as it does on every other /api/ path.
func TestUnknownAPIPathReturnsJSONNotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "unknown where path", path: "/api/where/nonexistent.json?key=TEST", wantStatus: http.StatusNotFound},
		{name: "unknown GTFS-RT export", path: "/api/gtfs_realtime/nonexistent-for-agency/25.pb?key=TEST", wantStatus: http.StatusNotFound},
		{name: "no API key", path: "/api/where/nonexistent.json", wantStatus: http.StatusUnauthorized},
		{name: "invalid API key", path: "/api/where/nonexistent.json?key=WRONG", wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[models.ResponseModel](t, api, tt.path)

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
			assert.Equal(t, "no-cache, no-store, must-revalidate", resp.Header.Get("Cache-Control"))
			assert.Equal(t, tt.wantStatus, model.Code)
		})
	}
}

// The fallback must not shadow a registered endpoint.
func TestUnknownAPIPathFallbackKeepsRegisteredRoutes(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[models.ResponseModel](t, api, "/api/where/current-time.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
}
