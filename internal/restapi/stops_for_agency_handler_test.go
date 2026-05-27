package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

// stopsForAgencyURL builds the /stops-for-agency URL with key=TEST baked in.
// Extra query params are merged from optional url.Values arguments.
func stopsForAgencyURL(agencyID string, params ...url.Values) string {
	q := url.Values{"key": {"TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/stops-for-agency/" + agencyID + ".json?" + q.Encode()
}

func TestStopsForAgencyRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api,
		"/api/where/stops-for-agency/"+testdata.Raba.ID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, stopsForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	require.NotEmpty(t, model.Data.List, "expected stops for agency")

	validDirections := map[string]bool{"N": true, "NE": true, "E": true, "SE": true, "S": true, "SW": true, "W": true, "NW": true}
	stopsWithDirections := 0

	for i, stop := range model.Data.List {
		assert.NotEmpty(t, stop.ID, "stop[%d].ID", i)
		assert.NotZero(t, stop.Lat, "stop[%d].Lat", i)
		assert.NotZero(t, stop.Lon, "stop[%d].Lon", i)
		assert.NotEmpty(t, stop.Name, "stop[%d].Name", i)
		assert.NotNil(t, stop.RouteIDs, "stop[%d].RouteIDs", i)
		assert.NotNil(t, stop.StaticRouteIDs, "stop[%d].StaticRouteIDs", i)

		assert.True(t, strings.HasPrefix(stop.ID, testdata.Raba.ID+"_"),
			"stop[%d].ID should have agency prefix: %s", i, stop.ID)

		for j, routeID := range stop.RouteIDs {
			assert.True(t, strings.HasPrefix(routeID, testdata.Raba.ID+"_"),
				"stop[%d].RouteIDs[%d] should have agency prefix: %s", i, j, routeID)
		}

		if validDirections[stop.Direction] {
			stopsWithDirections++
		}
	}

	assert.Greater(t, stopsWithDirections, len(model.Data.List)/2,
		"Expected more than half of stops to have valid directions, got %d out of %d", stopsWithDirections, len(model.Data.List))

	assert.Contains(t, model.Data.List, testdata.Stop4062, "expected Stop4062 to be in the list")

	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)

	assert.Empty(t, model.Data.References.Situations)
	assert.Empty(t, model.Data.References.StopTimes)
	assert.Empty(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Trips)

	assert.False(t, model.Data.LimitExceeded)
}

func TestStopsForAgencyInvalidAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, stopsForAgencyURL("invalid"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
}
