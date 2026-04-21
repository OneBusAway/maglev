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

func TestAgencyHandlerReturns400OnBlankID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/agency/ .json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Equal(t, "id contains invalid characters", model.Text) // Matches Maglev's exact validator output

	data, ok := model.Data.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, data, "fieldErrors")
}

func TestAgencyHandlerRejectsInvalidVersion(t *testing.T) {
	// Extension 5b: The caller supplies an unrecognised version parameter value.
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID

	// Supply an explicitly invalid version (e.g., 3)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/agency/"+agencyID+".json?key=TEST&version=3")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, model.Code)
	assert.Contains(t, model.Text, "unknown version")
	assert.Nil(t, model.Data)
}

func TestAgencyHandlerIgnoresIncludeReferencesFlag(t *testing.T) {
	// The includeReferences flag should have no observable effect, and references should remain empty.
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/agency/"+agencyID+".json?key=TEST&includeReferences=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok)

	// Assert references is present and empty
	references, ok := data["references"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, references["agencies"])
	assert.Empty(t, references["routes"])
	assert.Empty(t, references["stops"])
	assert.Empty(t, references["trips"])
	assert.Empty(t, references["situations"])
}
