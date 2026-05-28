package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
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

	assert.Equal(t, 2, model.Version)
	assert.Greater(t, model.CurrentTime, int64(0))

	expected := make([]string, 0, len(testdata.RabaRoutes))
	for _, r := range testdata.RabaRoutes {
		expected = append(expected, r.ID)
	}
	assert.ElementsMatch(t, expected, model.Data.List)

	assert.False(t, model.Data.LimitExceeded)

	assert.Empty(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Trips)
	assert.Empty(t, model.Data.References.Situations)
	assert.Empty(t, model.Data.References.StopTimes)
}

func TestInvalidAgencyIdForRouteIds(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/invalid.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestMalformedAgencyIdForRouteIds(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/bad@agency.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRouteIdsForAgencyInvalidVersion(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/"+testdata.Raba.ID+".json?key=TEST&version=99")

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, model.Code)
	assert.Equal(t, "unknown version: 99", model.Text)
}

func TestAgencyWithNoRoutesReturnsEmptyList(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()

	_, err := api.GtfsManager.GtfsDB.Queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       "no-routes-agency",
		Name:     "No Routes Agency",
		Url:      "http://example.com",
		Timezone: "America/New_York",
		Lang:     sql.NullString{String: "en", Valid: true},
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, "DELETE FROM agencies WHERE id = ?", "no-routes-agency")
	})

	resp, model := callAPIHandler[RouteIDsForAgencyResponse](t, api, "/api/where/route-ids-for-agency/no-routes-agency.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Empty(t, model.Data.List)
	assert.False(t, model.Data.LimitExceeded)
}
