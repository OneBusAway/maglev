package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func searchStopsURL(params url.Values) string {
	q := url.Values{"key": {"TEST"}}
	maps.Copy(q, params)
	return "/api/where/search/stop.json?" + q.Encode()
}

func TestSearchStopsHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, "/api/where/search/stop.json?input=test")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, stopsResp.Code)
	assert.Equal(t, "permission denied", stopsResp.Text)

	resp, _ = callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"test"}, "key": {"invalid"}}))
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSearchStopsHandlerMissingInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, stopsResp.Data.FieldErrors, "input")
}

func TestSearchStopsHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)
	assert.Equal(t, "OK", stopsResp.Text)

	require.NotEmpty(t, stopsResp.Data.List)
	assert.False(t, stopsResp.Data.LimitExceeded)
	assert.False(t, stopsResp.Data.OutOfRange)

	for _, stop := range stopsResp.Data.List {
		assert.NotEmpty(t, stop.ID)
		assert.NotEmpty(t, stop.Name)
		assert.NotZero(t, stop.Lat)
		assert.NotZero(t, stop.Lon)
	}

	assert.NotEmpty(t, stopsResp.Data.References.Agencies)
}

func TestSearchStopsHandlerNoResults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"NonExistentStopName12345"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List)
}

func TestSearchStopsHandlerMaxCount(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}, "maxCount": {"1"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.LessOrEqual(t, len(stopsResp.Data.List), 1)
}

func TestSearchStopsHandlerWhitespaceOnlyInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"    "}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List)
}

func TestSearchStopsHandlerSpecialCharactersOnly(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {`*()"`}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List)
}

func TestSearchStopsHandlerMaxCountBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		maxCount       string
		expectedStatus int
		expectError    bool
	}{
		{"omitted", "", http.StatusOK, false},
		{"valid", "10", http.StatusOK, false},
		{"zero", "0", http.StatusBadRequest, true},
		{"negative", "-1", http.StatusBadRequest, true},
		{"tooLarge", "251", http.StatusBadRequest, true},
		{"nonInteger", "abc", http.StatusBadRequest, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApi(t)
			defer api.Shutdown()

			params := url.Values{"input": {"Buenaventura"}}
			if tt.maxCount != "" {
				params.Set("maxCount", tt.maxCount)
			}
			resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(params))

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			if tt.expectError {
				assert.Contains(t, stopsResp.Data.FieldErrors, "maxCount")
			} else {
				assert.NotEmpty(t, stopsResp.Data.List)
			}
		})
	}
}

func TestSearchStopsHandlerFTSInjectionAttempt(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {`test" OR "1"="1`}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.LessOrEqual(t, len(stopsResp.Data.List), 20)
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"whitespace only", "     ", ""},
		{"all special characters", `"*()`, ""},
		{"mixed case operators AND", "test AND foo", "test foo"},
		{"mixed case operators And", "test And foo", "test foo"},
		{"mixed case operators aNd", "test aNd foo", "test foo"},
		{"consecutive operators", "foo AND AND bar", "foo bar"},
		{"operator at beginning", "AND test", "test"},
		{"operator at end", "test OR", "test"},
		{"unicode input", "中央駅 テスト", "中央駅 テスト"},
		{"colon character", "column:value", "column value"},
		{"caret character", "test^2", "test 2"},
		{"curly braces", "test{foo}bar", "test foo bar"},
		{"square brackets", "test[foo]bar", "test foo bar"},
		{"angle brackets", "test<foo>bar", "test foo bar"},
		{"tilde character", "test~2", "test 2"},
		{"pipe character", "test|foo", "test foo"},
		{"NEAR operator", "test NEAR foo", "test foo"},
		{"NEAR operator mixed case", "test near foo", "test foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := sanitizeFTS5Query(tt.input)
			assert.Equal(t, tt.expected, out)
		})
	}
}

func TestSearchStopsHandlerMultiWordWithSymbols(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Query "montg @ lib" tests that special chars are sanitized (@ removed) and multi-word prefix matching works.
	// Based on testdata (raba.zip), we expect this to match stop ID "25_8006" ("Montgomery Creek (SR 299 @ Montgomery Creek Library)").
	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"montg @ lib"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)
	assert.NotEmpty(t, stopsResp.Data.List, "Expected prefix intersection matching to return results")

	matched := false
	for _, stop := range stopsResp.Data.List {
		if stop.ID == "25_8006" && strings.Contains(stop.Name, "Montgomery") && strings.Contains(stop.Name, "Library") {
			matched = true
			break
		}
	}
	assert.True(t, matched, "Expected results to contain stop ID '25_8006' ('Montgomery Creek (SR 299 @ Montgomery Creek Library)')")
}

func TestSearchStopsHandlerIgnoredPunctuation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// The hyphen is not stripped by sanitization but is filtered out by term extraction
	// since it lacks alphanumeric characters. This triggers the empty-query path.
	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"-"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)
	assert.Empty(t, stopsResp.Data.List)
}

func TestSearchStopsHandlerReferencesSorting(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)

	routes := stopsResp.Data.References.Routes
	require.GreaterOrEqual(t, len(routes), 2, "expected at least two routes to verify sorting behavior")
	for i := 1; i < len(routes); i++ {
		keyA := routes[i-1].ShortName
		if keyA == "" {
			keyA = routes[i-1].LongName
		}
		keyB := routes[i].ShortName
		if keyB == "" {
			keyB = routes[i].LongName
		}
		assert.LessOrEqual(t, utils.NaturalCompare(keyA, keyB), 0, "routes inside references must be naturally sorted by ShortName/LongName")
	}

	agencies := stopsResp.Data.References.Agencies
	require.NotEmpty(t, agencies, "expected at least one agency in references")
	for i := 1; i < len(agencies); i++ {
		assert.LessOrEqual(t, agencies[i-1].ID, agencies[i].ID, "agencies inside references must be sorted alphabetically by ID")
	}
}

func TestSearchStopsHandlerIncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{
		"input":             {"Buenaventura"},
		"includeReferences": {"false"},
	}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)

	// We should still get stops in the list
	require.NotEmpty(t, stopsResp.Data.List)

	// But all reference arrays should be completely empty
	assert.Empty(t, stopsResp.Data.References.Agencies)
	assert.Empty(t, stopsResp.Data.References.Routes)
	assert.Empty(t, stopsResp.Data.References.Situations)
	assert.Empty(t, stopsResp.Data.References.Stops)
}

func TestSearchStopsHandlerLimitExceeded(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Establish a baseline of total available records for the test query.
	respAll, stopsRespAll := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}, "maxCount": {"100"}}))
	require.Equal(t, http.StatusOK, respAll.StatusCode)
	require.False(t, stopsRespAll.Data.LimitExceeded, "Expected LimitExceeded to be false when retrieving the complete result set")

	totalMatches := len(stopsRespAll.Data.List)
	require.Greater(t, totalMatches, 1, "Test requires a minimum of 2 matching records in the mock data")

	// Verify strict boundary condition where the requested limit equals total records.
	exactCountStr := strconv.Itoa(totalMatches)
	respExact, stopsRespExact := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}, "maxCount": {exactCountStr}}))
	assert.Equal(t, http.StatusOK, respExact.StatusCode)
	assert.False(t, stopsRespExact.Data.LimitExceeded, "Expected LimitExceeded to be false when maxCount exactly matches total available records")
	assert.Len(t, stopsRespExact.Data.List, totalMatches)

	// Verify pagination flag triggers correctly when results exceed the requested limit.
	exceededCountStr := strconv.Itoa(totalMatches - 1)
	respExceeded, stopsRespExceeded := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}, "maxCount": {exceededCountStr}}))
	assert.Equal(t, http.StatusOK, respExceeded.StatusCode)
	assert.True(t, stopsRespExceeded.Data.LimitExceeded, "Expected LimitExceeded to be true when available records exceed maxCount")
	assert.Len(t, stopsRespExceeded.Data.List, totalMatches-1)
}

func TestSearchStopsHandlerRouteTypeExclusion(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	db := api.GtfsManager.GtfsDB.DB

	// Insert mock data for exclusion testing
	_, err := db.Exec(`
		-- Stop with 0 routes
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('zero_route_stop', 'Ghost Stop', 40.0, -120.0, 0);

		-- Stop with 1 school bus route (type 712)
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('school_bus_stop', 'Single Special Stop', 40.0, -120.0, 0);
		INSERT OR IGNORE INTO agencies (id, name, url, timezone) VALUES ('RABA', 'RABA', 'http://raba.com', 'America/Los_Angeles');
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('school_route_1', 'RABA', 'School Route', 712);
		INSERT OR IGNORE INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date) VALUES ('service_1', 1, 1, 1, 1, 1, 1, 1, '20230101', '20251231');
		INSERT INTO trips (id, route_id, service_id) VALUES ('school_trip_1', 'school_route_1', 'service_1');
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES ('school_trip_1', 'school_bus_stop', 1, 28800, 28800);

		-- Stop with 1 valid route (type 3 - Bus)
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('valid_bus_stop', 'Valid Bus Stop', 40.0, -120.0, 0);
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('valid_route_1', 'RABA', 'Valid Route', 3);
		INSERT INTO trips (id, route_id, service_id) VALUES ('valid_trip_1', 'valid_route_1', 'service_1');
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES ('valid_trip_1', 'valid_bus_stop', 1, 28800, 28800);

		-- Stop with 2 school bus routes (type 712) to test allSpecial logic
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('two_school_routes_stop', 'Double School Bus Stop', 40.0, -120.0, 0);
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('school_route_2', 'RABA', 'School Route 2', 712);
		INSERT INTO trips (id, route_id, service_id) VALUES ('school_trip_2', 'school_route_2', 'service_1');
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES ('school_trip_1', 'two_school_routes_stop', 2, 28800, 28800);
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES ('school_trip_2', 'two_school_routes_stop', 1, 28800, 28800);

		-- Stops for maxCount filtering test
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('limit_ghost_1', 'Limit Test Ghost 1', 40.0, -120.0, 0);
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('limit_ghost_2', 'Limit Test Ghost 2', 40.0, -120.0, 0);
		INSERT INTO stops (id, name, lat, lon, location_type) VALUES ('limit_valid_3', 'Limit Test Valid 3', 40.0, -120.0, 0);
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time) VALUES ('valid_trip_1', 'limit_valid_3', 2, 28800, 28800);
	`)
	require.NoError(t, err)

	// Test 0 routes exclusion
	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Ghost"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List, "Expected Ghost Stop to be excluded (0 routes)")

	// Test School bus exclusion
	resp, stopsResp = callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Single Special"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List, "Expected Single Special Stop to be excluded (single route type 712)")

	// Test valid bus inclusion
	resp, stopsResp = callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Valid Bus"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stopsResp.Data.List, 1, "Expected Valid Bus Stop to be included")
	assert.True(t, strings.HasSuffix(stopsResp.Data.List[0].ID, "valid_bus_stop"))

	// Test inclusion of stop with multiple special routes (matching legacy defect #3)
	resp, stopsResp = callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Double School"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stopsResp.Data.List, 1, "Expected Double School Bus Stop to be included despite all routes being special")
	assert.True(t, strings.HasSuffix(stopsResp.Data.List[0].ID, "two_school_routes_stop"))

	// Test maxCount filtering scenario
	resp, stopsResp = callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Limit Test"}, "maxCount": {"2"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, stopsResp.Data.LimitExceeded, "Expected LimitExceeded to be true because FTS query matched 3 limits")
	// The final list will have length <= 2. Because FTS limits to 2+1=3, PaginateSlice truncates to 2.
	// Out of the 2, at least one (limit_ghost_1 or limit_ghost_2) will be filtered out.
	assert.LessOrEqual(t, len(stopsResp.Data.List), 1, "Expected at most 1 item after filtering truncated results")
}
