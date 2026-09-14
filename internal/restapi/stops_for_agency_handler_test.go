package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
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

// TestStopsForAgencyIncludesCrossAgencyRouteOwner covers the response-level
// invariant that every references.routes[].agencyId must resolve against
// references.agencies. A stop can be served by routes from more than one
// agency; when stops-for-agency/A1 surfaces a route owned by A2, A2 itself
// must also appear in references.agencies, not just A1.
func TestStopsForAgencyIncludesCrossAgencyRouteOwner(t *testing.T) {
	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"A1,Agency One,http://agency1.com,America/Los_Angeles\n" +
			"A2,Agency Two,http://agency2.com,America/Los_Angeles\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"r100,A1,100-A1,Route 100 For Agency 1,3\n" +
			"r300,A2,300-A2,Route 300 For Agency 2,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc1,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"s1,Shared Stop,37.7749,-122.4194\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id\n" +
			"r100,svc1,t1,A1 Headsign,0\n" +
			"r300,svc1,t2,A2 Headsign,0\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"t1,08:00:00,08:00:00,s1,1\n" +
			"t2,09:00:00,09:00:00,s1,1\n",
	}

	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "cross-agency-stops.zip", files)

	resp, model := callAPIHandler[StopsResponse](t, api, stopsForAgencyURL("A1"))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "expected the shared stop to be returned for A1")

	agencyIDs := make(map[string]bool)
	for _, a := range model.Data.References.Agencies {
		agencyIDs[a.ID] = true
	}
	assert.True(t, agencyIDs["A1"], "references.agencies must include the requested agency")
	assert.True(t, agencyIDs["A2"], "references.agencies must include A2, which owns a route referenced by the shared stop")

	require.NotEmpty(t, model.Data.References.Routes, "expected route references for the shared stop")
	for _, route := range model.Data.References.Routes {
		assert.True(t, agencyIDs[route.AgencyID],
			"route %s has agencyId %s which is not present in references.agencies", route.ID, route.AgencyID)
	}
}

func TestStopsForAgencyInvalidAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, stopsForAgencyURL("invalid"))

	// Maglev intentionally corrects the legacy 200+null behavior: an unknown
	// agency returns 404, consistent with stop-ids-for-agency and other endpoints.
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}
