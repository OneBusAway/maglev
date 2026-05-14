package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestRouteIdsForAgencyRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/test.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteIdsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/"+testdata.Raba.ID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	expected := make([]string, 0, len(testdata.RabaRoutes))
	for _, r := range testdata.RabaRoutes {
		expected = append(expected, r.ID)
	}
	assert.ElementsMatch(t, expected, model.Data.List)
	assert.Empty(t, model.Data.References.Agencies)
}

func TestInvalidAgencyIdForRouteIds(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/invalid.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
}

func TestMalformedAgencyIdForRouteIds(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/bad@agency.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}
