package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopIdsForAgencyRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stop-ids-for-agency/test.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopIdsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop-ids-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	for _, stopId := range list {
		stopIdStr, ok := stopId.(string)
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(stopIdStr, agencyId+"_"),
			"Stop ID should start with agency ID prefix: %s", stopIdStr)
	}

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agencyRefs, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, agencyRefs, 0)
}

func TestInvalidAgencyId(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stop-ids-for-agency/invalid.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
}
