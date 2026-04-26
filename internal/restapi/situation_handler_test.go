package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSituationHandlerRequiresValidAPIKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/test-alert.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestSituationHandlerNotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/nonexistent-alert.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestSituationHandlerWithSituation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	alert := gtfs.Alert{
		ID: "test-alert-123",
		Header: []gtfs.AlertText{
			{Text: "Service disruption", Language: "en"},
		},
		Description: []gtfs.AlertText{
			{Text: "Detour in effect", Language: "en"},
		},
	}
	api.GtfsManager.AddTestAlert(alert)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/test-alert-123.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response should include data object")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "response should include data.entry object")
	assert.Equal(t, "test-alert-123", entry["id"])
	assert.Equal(t, "UNKNOWN_CAUSE", entry["reason"])
	assert.Equal(t, "noImpact", entry["severity"])

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "response should include data.references object")

	agencies, ok := references["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, agencies, 0)

	routes, ok := references["routes"].([]interface{})
	require.True(t, ok)
	assert.Len(t, routes, 0)

	stops, ok := references["stops"].([]interface{})
	require.True(t, ok)
	assert.Len(t, stops, 0)
}
