package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
)

// Unknown API paths, such as GTFS-RT exports this server doesn't serve yet, must 404
// with the JSON envelope rather than fall through to the web UI's landing page.
func TestUnknownAPIPathReturnsJSONNotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	for _, path := range []string{
		"/api/where/nonexistent.json?key=TEST",
		"/api/gtfs_realtime/alerts-for-agency/25.pb?key=TEST",
	} {
		t.Run(path, func(t *testing.T) {
			resp, model := callAPIHandler[models.ResponseModel](t, api, path)

			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
			assert.Equal(t, http.StatusNotFound, model.Code)
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
