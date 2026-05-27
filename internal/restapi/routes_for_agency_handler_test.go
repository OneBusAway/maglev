package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestRoutesForAgencyHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRoutesForAgencyHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.ElementsMatch(t, testdata.RabaRoutes, model.Data.List)
	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
}

func TestRoutesForAgencyHandlerInvalidID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "11@10"
	endpoint := "/api/where/routes-for-agency/" + malformedID + ".json?key=TEST"

	resp, model := callAPIHandler[RoutesResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Contains(t, model.Text, "invalid")
}

func TestRoutesForAgencyHandlerNonExistentAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/non-existent-agency.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestRoutesForAgencyHandlerReturnsCompoundRouteIDs(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List)

	for _, route := range model.Data.List {
		assert.True(t, strings.HasPrefix(route.ID, "25_"), "route id must be in {agencyId}_{routeId} format")
	}
}

func TestRoutesForAgencyHandlerPagination(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	var results []models.Route
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=TEST&limit=5")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 5)
	assert.True(t, model.Data.LimitExceeded)
	results = append(results, model.Data.List...)

	resp, model = callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=TEST&offset=5&limit=5")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 5)
	assert.True(t, model.Data.LimitExceeded)
	results = append(results, model.Data.List...)

	resp, model = callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=TEST&offset=10&limit=5")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 3)
	assert.False(t, model.Data.LimitExceeded)
	results = append(results, model.Data.List...)

	assert.ElementsMatch(t, testdata.RabaRoutes, results)
}

func TestRoutesForAgencyHandlerLimitExceedsMax(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/25.json?key=TEST&limit=100")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	assert.ElementsMatch(t, testdata.RabaRoutes, model.Data.List)
	assert.False(t, model.Data.LimitExceeded, "limitExceeded should be false when all items returned")
}
