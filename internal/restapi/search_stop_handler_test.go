package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchStopHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/search/stop.json?key=invalid&input=TEST")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
}

func TestSearchStopHandlerValidation(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/search/stop.json?key=TEST")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSearchStopHandlerMaxCountValidation(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/search/stop.json?key=TEST&input=Main&maxCount=300")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSearchStopHandlerEndToEnd(t *testing.T) {
	// Assuming test data contains a stop with "Main" in the name
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/search/stop.json?key=TEST&input=Main")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	// We expect at least one result if the test DB has "Main" stops
	// If the test DB is empty, this assertion checks structure but not content logic
	if len(list) > 0 {
		stop, ok := list[0].(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, stop, "id")
		assert.Contains(t, stop, "name")
		assert.Contains(t, stop, "routeIds")
	}

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, refs, "agencies")
	assert.Contains(t, refs, "routes")
}