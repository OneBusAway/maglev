package restapi

import (
	"context"
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

func TestSearchStopsHandlerParentStationReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()

	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, wheelchair_boarding)
		VALUES ('parent_stat_1', 'P1', 'Parent Station One', 40.0, -120.0, 1, 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, parent_station, wheelchair_boarding)
		VALUES ('child_stop_1', 'C1', 'Child Stop One', 40.0, -120.0, 0, 'parent_stat_1', 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO routes (id, agency_id, short_name, type)
		VALUES ('route_parent_test', '999', 'RT-P', 3)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('trip_parent_test', 'route_parent_test', 'service_1')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_parent_test', 'child_stop_1', 1, 28800, 28800)
	`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM stop_times WHERE trip_id = 'trip_parent_test'`)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM trips WHERE id = 'trip_parent_test'`)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM routes WHERE id = 'route_parent_test'`)
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `DELETE FROM stops WHERE id IN ('child_stop_1', 'parent_stat_1')`)
	})

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Child Stop One"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)

	require.Len(t, stopsResp.Data.List, 1)
	childStop := stopsResp.Data.List[0]
	assert.Equal(t, "999_child_stop_1", childStop.ID)
	assert.Equal(t, "999_parent_stat_1", childStop.Parent)

	require.Len(t, stopsResp.Data.References.Stops, 1, "Expected 1 parent station in references.stops")
	parentStop := stopsResp.Data.References.Stops[0]
	assert.Equal(t, "999_parent_stat_1", parentStop.ID)
	assert.Equal(t, "Parent Station One", parentStop.Name)
}
