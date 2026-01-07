package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteIdsForAgencyRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/route-ids-for-agency/test.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteIdsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].Id

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/route-ids-for-agency/"+agencyId+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})

	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	for _, routeId := range list {
		routeIdStr, ok := routeId.(string)
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(routeIdStr, agencyId+"_"),
			"Route ID should start with agency ID prefix: %s", routeIdStr)
	}

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)
	agencyRefs, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, agencyRefs, 0)
}

func TestInvalidAgencyIdForRouteIds(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/route-ids-for-agency/invalid.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
}
