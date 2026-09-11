package restapi

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"testing"

	gogtfs "github.com/OneBusAway/go-gtfs"
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

func TestRoutesForLocationHandlerIncludeReferences(t *testing.T) {
	const baseURL = "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966"

	tests := []struct {
		name           string
		params         string
		wantReferences bool
	}{
		{"includeReferences=false suppresses references", "&includeReferences=false", false},
		{"includeReferences=true populates references", "&includeReferences=true", true},
		{"includeReferences absent defaults to populated", "", true},
		{"includeReferences unparseable defaults to populated", "&includeReferences=notabool", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApi(t)

			resp, model := callAPIHandler[RoutesResponse](t, api, baseURL+tt.params)

			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.ElementsMatch(t, []models.Route{testdata.Route19}, model.Data.List, "list must be unaffected by includeReferences")

			if tt.wantReferences {
				assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
			} else {
				assert.Empty(t, model.Data.References.Agencies)
				assert.Empty(t, model.Data.References.Situations)
			}
		})
	}
}

// TestRoutesForLocationHandlerSituationReferencesStayEmpty pins the spec's
// references contract: only agencies is populated. An alert affecting a returned
// route must not surface here, because route beans carry no situation IDs to
// resolve — callers wanting alerts fetch them from a route- or trip-scoped
// endpoint instead.
func TestRoutesForLocationHandlerSituationReferencesStayEmpty(t *testing.T) {
	api := createTestApi(t)

	// testdata.Route19's combined ID is "25_3779"; alerts are matched on the raw
	// (un-prefixed) route ID, so this alert does affect a route the search returns.
	rawRouteID := "3779"
	api.GtfsManager.AddAlertForTest(gogtfs.Alert{
		ID:               "test-alert-routes-for-location",
		InformedEntities: []gogtfs.AlertInformedEntity{{RouteID: &rawRouteID}},
		Header:           []gogtfs.AlertText{{Text: "Test Route Alert", Language: "en"}},
	})

	const baseURL = "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966"

	_, model := callAPIHandler[RoutesResponse](t, api, baseURL)

	assert.ElementsMatch(t, []models.Route{testdata.Route19}, model.Data.List,
		"the alerted route is still returned in the list")
	assert.Empty(t, model.Data.References.Situations)
	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies,
		"agencies remain the one populated reference array")
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
			// maxCount is set explicitly above the RABA fixture's route count so this
			// case stays about box sizing; the endpoint's default maxCount (10) would
			// otherwise randomly truncate these 13 matches on every run.
			name:             "spans widen the box beyond the default radius",
			params:           denseCentre + "&latSpan=0.1&lonSpan=0.1&maxCount=50",
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
			// stay the 10km query radius, not the current no-query default (600m; see
			// TestRoutesForLocationDefaultRadiusMatchesCurrentConstant). Both routes matching
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

// TestRoutesForLocationDefaultRadiusMatchesCurrentConstant pins the no-query default
// radius against models.DefaultSearchRadiusInMeters (currently 600m) by equivalence
// rather than a hardcoded distance: this RABA stop has a neighboring stop whose routes
// only enter the box between 500m and 600m, so a request with no radius/span must match
// radius=600 exactly and return strictly more routes than radius=500.
//
// 600m is current behavior, not the spec: the routes-for-location wiki page and the
// hosted Java API both give 500m as the no-query default. This test's literals must
// change if/when models.DefaultSearchRadiusInMeters is corrected to 500 — see
// https://github.com/OneBusAway/maglev/issues/1227.
func TestRoutesForLocationDefaultRadiusMatchesCurrentConstant(t *testing.T) {
	const lat, lon = 40.618458, -122.37598

	routeIDsFor := func(t *testing.T, params string) []string {
		t.Helper()
		api := createTestApi(t)
		_, model := callAPIHandler[RoutesResponse](t, api,
			fmt.Sprintf("/api/where/routes-for-location.json?key=TEST&lat=%v&lon=%v&maxCount=50%s", lat, lon, params))
		ids := make([]string, 0, len(model.Data.List))
		for _, route := range model.Data.List {
			ids = append(ids, route.ID)
		}
		return ids
	}

	defaultIDs := routeIDsFor(t, "")
	radius500IDs := routeIDsFor(t, "&radius=500")
	radius600IDs := routeIDsFor(t, "&radius=600")

	assert.ElementsMatch(t, radius600IDs, defaultIDs, "no radius/span must match radius=600 exactly")
	assert.Subset(t, defaultIDs, radius500IDs, "every radius=500 route must still be within the default radius")
	assert.Greater(t, len(defaultIDs), len(radius500IDs), "the default radius must include routes radius=500 misses")
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
			name:       "multiple terms both matching one route",
			url:        "query=Shasta+College&" + stop2000,
			wantRoutes: []models.Route{testdata.Route17},
		},
		{
			name:       "terms are ORed so either may match",
			url:        "query=Shasta+Airport&" + stop2000,
			wantRoutes: []models.Route{testdata.Route17, testdata.Route15},
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

// seedRoutesNearLocation inserts count synthetic routes that all serve the stop
// nearest to (lat, lon), so that route-count caps above the RABA fixture's 13
// routes become observable. Rows are removed via t.Cleanup.
func seedRoutesNearLocation(t *testing.T, api *RestAPI, lat, lon float64, count int) {
	t.Helper()
	ctx := context.Background()
	db := api.GtfsManager.GtfsDB.DB

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies, "test data should contain at least one agency")
	agencyID := agencies[0].ID

	var serviceID string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM calendar LIMIT 1`).Scan(&serviceID))

	var stopID string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id FROM stops ORDER BY (lat-?)*(lat-?)+(lon-?)*(lon-?) LIMIT 1`,
		lat, lat, lon, lon,
	).Scan(&stopID))

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DELETE FROM stop_times WHERE trip_id LIKE 'clamp-test-%'`)
		_, _ = db.ExecContext(ctx, `DELETE FROM trips WHERE id LIKE 'clamp-test-%'`)
		_, _ = db.ExecContext(ctx, `DELETE FROM routes WHERE id LIKE 'clamp-test-%'`)
	})

	for i := range count {
		id := fmt.Sprintf("clamp-test-%d", i)
		_, err := db.ExecContext(ctx,
			`INSERT INTO routes (id, agency_id, short_name, type) VALUES (?, ?, ?, 3)`,
			id, agencyID, id)
		require.NoError(t, err)

		_, err = db.ExecContext(ctx,
			`INSERT INTO trips (id, route_id, service_id) VALUES (?, ?, ?)`,
			id, id, serviceID)
		require.NoError(t, err)

		_, err = db.ExecContext(ctx,
			`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence) VALUES (?, 0, 0, ?, 0)`,
			id, stopID)
		require.NoError(t, err)
	}
}

func TestRoutesForLocationHandlerClampsMaxCountAboveCap(t *testing.T) {
	const lat, lon = 40.583321, -122.362535

	tests := []struct {
		name        string
		maxCount    string
		wantLen     int
		wantExceeds bool
	}{
		{"below endpoint cap", "10", 10, true},
		{"at endpoint cap", "50", 50, true},
		{"just above endpoint cap", "51", 50, true},
		{"at global ceiling", "250", 50, true},
		{"above global ceiling", "300", 50, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApi(t)
			// Seed enough routes near the query point that the endpoint's 50 cap
			// (and the fact it's 50, not 250) is actually observable - RABA alone
			// only has 13 routes total.
			seedRoutesNearLocation(t, api, lat, lon, 60)

			resp, model := callAPIHandler[RoutesResponse](t, api,
				fmt.Sprintf("/api/where/routes-for-location.json?key=TEST&lat=%v&lon=%v&radius=5000&maxCount=%s", lat, lon, tt.maxCount))

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
			assert.Len(t, model.Data.List, tt.wantLen)
			assert.Equal(t, tt.wantExceeds, model.Data.LimitExceeded)
		})
	}
}

func TestRoutesForLocationHandlerDefaultsMaxCountToTen(t *testing.T) {
	const lat, lon = 40.583321, -122.362535
	api := createTestApi(t)
	// Seed past 10 in-bounds routes so the spec default (10, not the endpoint's
	// 50 clamp ceiling) is actually observable.
	seedRoutesNearLocation(t, api, lat, lon, 15)

	resp, model := callAPIHandler[RoutesResponse](t, api,
		fmt.Sprintf("/api/where/routes-for-location.json?key=TEST&lat=%v&lon=%v&radius=5000", lat, lon))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Len(t, model.Data.List, 10)
	assert.True(t, model.Data.LimitExceeded)
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

// maxCountFieldErrors extracts the fieldErrors["maxCount"] list from a validation
// error response body.
func maxCountFieldErrors(t *testing.T, data any) []any {
	t.Helper()

	body, ok := data.(map[string]any)
	require.True(t, ok, "response data should be a map")

	fieldErrors, ok := body["fieldErrors"].(map[string]any)
	require.True(t, ok, "data should contain fieldErrors map")

	maxCountErrors, ok := fieldErrors["maxCount"].([]any)
	require.True(t, ok, "fieldErrors should contain maxCount errors list")

	return maxCountErrors
}

// TestRoutesForLocationHandlerInvalidMaxCount pins the exact fieldErrors message for
// each invalid maxCount case, not just the status code. The wiki page's extension 1a
// quotes "must be greater than zero" for maxCount <= 0; the message itself is defined
// in internal/utils/api.go.
func TestRoutesForLocationHandlerInvalidMaxCount(t *testing.T) {
	tests := []struct {
		name        string
		maxCount    string
		wantMessage string
	}{
		{"zero", "0", "must be greater than zero"},
		{"negative", "-5", "must be greater than zero"},
		{"unparseable", "invalid", `Invalid field value for field "maxCount".`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApi(t)
			resp, model := callAPIHandler[models.ResponseModel](t, api,
				"/api/where/routes-for-location.json?key=TEST&lat=40.621&lon=-122.571&maxCount="+tt.maxCount)

			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
			assert.Contains(t, maxCountFieldErrors(t, model.Data), tt.wantMessage)
		})
	}
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
