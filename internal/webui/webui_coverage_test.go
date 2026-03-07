package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetWebUIRoutesCoverage(t *testing.T) {
	mux := http.NewServeMux()

	// Create an instance of the WebUI struct
	ui := &WebUI{}

	// Call SetWebUIRoutes as a method on the struct
	ui.SetWebUIRoutes(mux)

	// Make an HTTP request to verify the mux has registered handlers
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	// If the mux is empty, it returns 404. Since we registered routes,
	// the index or a valid handler should catch it.
	assert.NotEqual(t, http.StatusNotFound, rec.Code, "Expected a registered handler for /")
}
