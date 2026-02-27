package restapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracingMiddleware_Integration(t *testing.T) {
	// This test ensures tracing middleware can be applied without errors
	// Actual span verification would require a test exporter

	api := createTestApi(t)
	defer api.GtfsManager.Shutdown()

	resp, httpResp, model := serveAndRetrieveEndpoint(t, api, "/api/where/agencies-with-coverage.json?key=TEST")

	require.NotNil(t, resp)
	assert.Equal(t, 200, httpResp.StatusCode)
	assert.Equal(t, 2, model.Version)
	assert.NotNil(t, model.Data)
}

func TestTracingMiddleware_HandlesErrors(t *testing.T) {
	// Test that tracing middleware properly handles error responses

	api := createTestApi(t)
	defer api.GtfsManager.Shutdown()

	// Request a non-existent stop
	_, httpResp, _ := serveAndRetrieveEndpoint(t, api, "/api/where/stop/INVALID_999.json?key=TEST")

	assert.Equal(t, 404, httpResp.StatusCode)
}
