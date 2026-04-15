package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgencyHandlerReturnsAgencyWhenItExists(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/agency/"+agencyID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, agencies[0].ID, entry["id"])
	assert.Equal(t, agencies[0].Name, entry["name"])
	assert.Equal(t, agencies[0].Url, entry["url"])
	assert.Equal(t, agencies[0].Timezone, entry["timezone"])
}

func TestAgencyHandlerReturnsNullWhenAgencyDoesNotExist(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/agency/non-existent-id.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestAgencyHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/agency/"+agencyID+".json?key=INVALID")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}
