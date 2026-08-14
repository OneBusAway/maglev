package restapi

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestRoutesForLocationHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/routes-for-location.json?key=invalid&lat=47.586556&lon=-122.190396")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRoutesForLocationHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route19})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationQuery(t *testing.T) {
	api := createTestApi(t)

	// Wider radius includes multiple routes, but query limits response to just 19.
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2000&query=19")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route19})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

// TestRoutesForLocationBoundingBoxSizing pins the sizing precedence from the spec:
// radius wins, then latSpan/lonSpan, then the query-aware default radius.
//
// Each case must produce a different result than the default box would, otherwise it
// cannot tell a working span implementation from one that silently ignores the spans.
func TestRoutesForLocationBoundingBoxSizing(t *testing.T) {
	// Both centres are RABA stops. At the default radius, sparseCentre serves one route
	// and denseCentre serves three; the counts below are relative to those baselines.
	const sparseCentre = "lat=40.583321&lon=-122.426966"
	const denseCentre = "lat=40.583321&lon=-122.362535"

	tests := []struct {
		name             string
		params           string
		expectedRouteIDs []string
	}{
		{
			name:             "spans widen the box beyond the default radius",
			params:           denseCentre + "&latSpan=0.1&lonSpan=0.1",
			expectedRouteIDs: []string{"25_15", "25_151", "25_153", "25_154", "25_157", "25_159", "25_160", "25_161", "25_1885", "25_24", "25_3779", "25_44X", "25_6446"},
		},
		{
			name:             "spans narrow the box below the default radius",
			params:           denseCentre + "&latSpan=0.0002&lonSpan=0.0002",
			expectedRouteIDs: []string{},
		},
		{
			name:             "radius takes precedence over spans",
			params:           sparseCentre + "&radius=2000&latSpan=0.5&lonSpan=0.5",
			expectedRouteIDs: []string{"25_153", "25_3779"},
		},
		{
			// Only one span is unusable, so the default radius still applies — and it must
			// stay the 10km query radius, not the 600m no-query one. Both routes matching
			// "3" sit outside 600m of denseCentre but inside 10km, so a regression to the
			// no-query radius empties the list. 25_24 matches on its long name
			// ("Route 99X/Amtrak Thruway Route 3"), not its short name.
			name:             "one span alone falls back to the query default radius",
			params:           denseCentre + "&latSpan=0.1&query=3",
			expectedRouteIDs: []string{"25_153", "25_24"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A fresh API per case: the shared TEST key is rate limited across requests.
			api := createTestApi(t)

			resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&"+tt.params)

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			// Other tests in this package write synthetic agencies into the shared test DB
			// and never remove them, so a wide box can pick them up depending on test order.
			// Only the RABA fixture is meaningful here.
			routeIDs := make([]string, 0, len(model.Data.List))
			for _, route := range model.Data.List {
				if route.AgencyID == testdata.Raba.ID {
					routeIDs = append(routeIDs, route.ID)
				}
			}
			assert.ElementsMatch(t, tt.expectedRouteIDs, routeIDs)
		})
	}
}

func TestRoutesForLocationRadius(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2000")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, model.Data.List, 2)
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationLatAndLon(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.362535")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Ordering matters! Routes should be sorted by ID.
	assert.EqualValues(t, model.Data.List, []models.Route{testdata.Route15, testdata.Route11, testdata.Route14})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationCaseInsensitiveQuery(t *testing.T) {
	// Lat/Lon are for stop 2000 from the test data, which is on route 44X
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583170&lon=-122.392586&query=44x")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.ElementsMatch(t, model.Data.List, []models.Route{testdata.Route44x})
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

func TestRoutesForLocationWildcardQueryDoesNotMatch(t *testing.T) {
	// `%` should be treated as a literal character, not a SQL LIKE wildcard.
	// Lat/Lon are for stop 2000 from the test data, which is on route 44X.
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583170&lon=-122.392586&query=%25")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Empty(t, model.Data.List)
}

func TestRoutesForLocationQueryMatchesRouteText(t *testing.T) {
	// lat/lon are for stop 2000, which is served by both Route44X and Route17.
	// Query mode defaults to a 10km radius, so any RABA route is reachable from here.
	const stop2000 = "lat=40.583170&lon=-122.392586"

	tests := []struct {
		name       string
		url        string
		wantRoutes []models.Route
	}{
		{
			name:       "matches long name only",
			url:        "query=Shasta&" + stop2000,
			wantRoutes: []models.Route{testdata.Route17},
		},
		{
			name:       "matches a long-name prefix token",
			url:        "query=Chur&" + stop2000,
			wantRoutes: []models.Route{testdata.Route15},
		},
		{
			name:       "multiple terms are ANDed and both must match",
			url:        "query=Shasta+College&" + stop2000,
			wantRoutes: []models.Route{testdata.Route17},
		},
		{
			name:       "multiple terms with one absent from any route match nothing",
			url:        "query=Shasta+Airport&" + stop2000,
			wantRoutes: []models.Route{},
		},
		{
			name:       "no route text matches the query",
			url:        "query=zzzz&" + stop2000,
			wantRoutes: []models.Route{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApi(t)
			resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&"+tt.url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
			assert.False(t, model.Data.LimitExceeded)
			assert.ElementsMatch(t, tt.wantRoutes, model.Data.List)
		})
	}
}

func TestRoutesForLocationQueryCandidatesAreCappedAtMaxCountPlusOne(t *testing.T) {
	// Ten RABA routes have a long name starting with "Route ", far more than
	// maxCount+1 candidates. With maxCount=1, only the top-ranked text-search
	// candidates are considered, so the result must be truncated and flagged.
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=Route&maxCount=1")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 1)
	assert.True(t, model.Data.LimitExceeded)
}

func TestRoutesForLocationQueryMatchIgnoredWhenOutOfBounds(t *testing.T) {
	// "Shasta" matches Route17's long name, but a tiny explicit radius far from
	// any RABA stop must still drop it: text-index candidates are filtered by
	// location, not returned unconditionally.
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=0.0&lon=0.0&radius=100&query=Shasta")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Empty(t, model.Data.List)
	assert.False(t, model.Data.LimitExceeded)
}

func TestRoutesForLocationHandlerValidatesParameters(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=invalid&lon=-121.74")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerValidatesLatLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=invalid&lon=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerValidatesLatLonSpan(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=invalid&lonSpan=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerValidatesRadius(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerNoStopsFound(t *testing.T) {
	// Use coordinates far from any stops to trigger the empty stopIDs case
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=0.0&lon=0.0&radius=100")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.Empty(t, model.Data.List)
	assert.False(t, model.Data.LimitExceeded)
	assert.True(t, model.Data.OutOfRange)

	refs := model.Data.References
	assert.Empty(t, refs.Agencies)
	assert.Empty(t, refs.Routes)
	assert.Empty(t, refs.Situations)
	assert.Empty(t, refs.StopTimes)
	assert.Empty(t, refs.Stops)
	assert.Empty(t, refs.Trips)
}

func TestRoutesForLocationHandlerLimitExceeded(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&maxCount=2")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.LimitExceeded)
	require.Len(t, model.Data.List, 2)
	// Truncation is randomized (per spec), so only check the returned routes
	// are drawn from the full in-bounds match set (confirmed by
	// TestRoutesForLocationLatAndLon: Route15, Route11, Route14).
	assert.Subset(t, []models.Route{testdata.Route15, testdata.Route11, testdata.Route14}, model.Data.List)
	// Ordering matters! Routes should still be sorted by ID after truncation.
	assert.True(t, model.Data.List[0].ID < model.Data.List[1].ID)
	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
}

// routesForLocationShuffleIterations bounds the flake probability of
// TestRoutesForLocationHandlerLimitExceededIsRandomized: with 3 candidate
// routes and maxCount=2, a specific route is dropped with probability 1/3 per
// call, so the odds of it never appearing across all iterations is
// (1/3)^routesForLocationShuffleIterations.
const routesForLocationShuffleIterations = 50

func TestRoutesForLocationHandlerLimitExceededIsRandomized(t *testing.T) {
	api := createTestApi(t)
	candidates := []models.Route{testdata.Route15, testdata.Route11, testdata.Route14}

	seen := map[string]bool{}
	for range routesForLocationShuffleIterations {
		// org.onebusaway.iphone is exempt from rate limiting; the "TEST" key's
		// low test rate limit can't sustain this many rapid-fire requests.
		resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=org.onebusaway.iphone&lat=40.583321&lon=-122.362535&maxCount=2")

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, model.Data.LimitExceeded)
		require.Len(t, model.Data.List, 2)
		assert.Subset(t, candidates, model.Data.List)
		assert.True(t, model.Data.List[0].ID < model.Data.List[1].ID)

		for _, route := range model.Data.List {
			seen[route.ID] = true
		}
	}

	assert.ElementsMatch(t, []string{testdata.Route15.ID, testdata.Route11.ID, testdata.Route14.ID}, slices.Collect(maps.Keys(seen)))
}

func TestRoutesForLocationHandlerInvalidMaxCount(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.621&lon=-122.571&maxCount=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerMaxCountLessThanOrEqualZero(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.621&lon=-122.571&maxCount=0")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestRoutesForLocationHandlerInRangeWithNoResults(t *testing.T) {
	api := createTestApi(t)
	boundsMap := api.GtfsManager.GetRegionBounds()
	// Pick any agency's bounds for the in-range test
	var bounds gtfs.RegionBounds
	for _, b := range boundsMap {
		bounds = b
		break
	}
	resp, model := callAPIHandler[RoutesResponse](t, api, fmt.Sprintf("/api/where/routes-for-location.json?key=TEST&lat=%v&lon=%v&radius=1", bounds.Lat, bounds.Lon))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, http.StatusOK, model.Code)

	assert.False(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
	assert.Empty(t, model.Data.References.Agencies)
}

func TestRoutesForLocationMissingLat(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lon=-122.426966")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
}

func TestRoutesForLocationMissingLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST&lat=40.583321")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
}

func TestRoutesForLocationMissingBothLatAndLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-location.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
}
