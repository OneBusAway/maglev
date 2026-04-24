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

	// Check optional fields that serialize to "" when empty
	if agencies[0].Lang.Valid && agencies[0].Lang.String != "" {
		assert.Equal(t, agencies[0].Lang.String, entry["lang"])
	} else {
		assert.Equal(t, "", entry["lang"])
	}

	if agencies[0].Phone.Valid && agencies[0].Phone.String != "" {
		assert.Equal(t, agencies[0].Phone.String, entry["phone"])
	} else {
		assert.Equal(t, "", entry["phone"])
	}

	if agencies[0].Email.Valid && agencies[0].Email.String != "" {
		assert.Equal(t, agencies[0].Email.String, entry["email"])
	} else {
		assert.Equal(t, "", entry["email"])
	}

	if agencies[0].FareUrl.Valid && agencies[0].FareUrl.String != "" {
		assert.Equal(t, agencies[0].FareUrl.String, entry["fareUrl"])
	} else {
		assert.Equal(t, "", entry["fareUrl"])
	}

	// In Maglev, disclaimer and privateService are hardcoded default values
	assert.Equal(t, "", entry["disclaimer"])
	assert.Equal(t, false, entry["privateService"])
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

