package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteSearchHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/search/route.json?key=invalid&input=1")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteSearchHandlerEndToEnd(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/search/route.json?key=TEST&input=shasta")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	found := false
	for _, item := range list {
		route, ok := item.(map[string]interface{})
		require.True(t, ok)
		assert.Contains(t, route, "id")
		assert.Contains(t, route, "agencyId")
		assert.Contains(t, route, "shortName")
		assert.Contains(t, route, "longName")
		assert.Contains(t, route, "type")

		if route["shortName"] == "17" {
			longName, _ := route["longName"].(string)
			assert.True(t, strings.Contains(strings.ToLower(longName), "shasta"))
			found = true
		}
	}
	assert.True(t, found, "expected Shasta route to be returned")

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, agencies)
}

func TestRouteSearchHandlerRequiresInput(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/search/route.json?key=TEST&input=")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Zero(t, model.Code)
}

func TestRouteSearchHandlerValidatesMaxCount(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/search/route.json?key=TEST&input=1&maxCount=-1")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Zero(t, model.Code)
}
