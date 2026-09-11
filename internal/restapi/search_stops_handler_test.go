package restapi

import (
	"context"
	"database/sql"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func searchStopsURL(params url.Values) string {
	q := url.Values{"key": {"TEST"}}
	maps.Copy(q, params)
	return "/api/where/search/stop.json?" + q.Encode()
}

// registerFixtureCleanup schedules fixture deletions before the inserts run, so the rows
// are still removed when an insert fails and aborts the test. The test database is shared
// by every test in the package run, so leftover rows leak into other tests' results.
func registerFixtureCleanup(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, statement := range statements {
			if _, err := db.ExecContext(context.Background(), statement); err != nil {
				t.Errorf("fixture cleanup failed for %q: %v", statement, err)
			}
		}
	})
}

// rebuildStopAgencyIndex refreshes the precomputed stop-to-agency index that stop search
// orders by. Tests insert fixture stops straight into the database, bypassing the import
// that builds the index in production.
func rebuildStopAgencyIndex(t *testing.T, api *RestAPI) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, api.GtfsManager.GtfsDB.Queries.ClearStopAgencies(ctx))
	require.NoError(t, api.GtfsManager.GtfsDB.Queries.BuildStopAgencies(ctx))
}

// clearIndexedStopAgencies removes the given stops from the index. Fixture cleanup has to
// run this before deleting the stops or agencies the index rows point at.
func clearIndexedStopAgencies(stopIDs string) string {
	return `DELETE FROM stop_agencies WHERE stop_id IN (` + stopIDs + `)`
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
		// Operator words are kept verbatim - the caller quotes every term, so FTS5 reads
		// them as literal tokens. Stop names such as "Park and Ride" depend on this.
		{"operator word AND preserved", "test AND foo", "test AND foo"},
		{"operator word And preserved", "test And foo", "test And foo"},
		{"operator word aNd preserved", "test aNd foo", "test aNd foo"},
		{"consecutive operator words preserved", "foo AND AND bar", "foo AND AND bar"},
		{"operator word at beginning preserved", "AND test", "AND test"},
		{"operator word at end preserved", "test OR", "test OR"},
		{"unicode input", "中央駅 テスト", "中央駅 テスト"},
		{"colon character", "column:value", "column value"},
		{"caret character", "test^2", "test 2"},
		{"curly braces", "test{foo}bar", "test foo bar"},
		{"square brackets", "test[foo]bar", "test foo bar"},
		{"angle brackets", "test<foo>bar", "test foo bar"},
		{"tilde character", "test~2", "test 2"},
		{"pipe character", "test|foo", "test foo"},
		{"operator word NEAR preserved", "test NEAR foo", "test NEAR foo"},
		{"operator word near preserved", "test near foo", "test near foo"},
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

func TestSearchStopsHandlerOperatorWordInput(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedStop string
	}{
		// FTS5 operator words must search as ordinary prefixes. Both stops come from the
		// RABA fixture, so no synthetic rows are needed.
		{"OR matches Oregon", "or", "25_1062"}, // "Shasta Dam Blvd at 5001 Oregon"
		{"AND matches and", "and", "25_4404"},  // "Shingletown Park and Ride Lot"
		{"NOT is a valid query", "not", ""},    // no RABA stop has a "not" prefix token
		{"NEAR is a valid query", "near", ""},  // no RABA stop has a "near" prefix token
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApi(t)
			defer api.Shutdown()

			resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {tt.input}}))

			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, stopsResp.Code)

			if tt.expectedStop == "" {
				return
			}

			ids := make([]string, 0, len(stopsResp.Data.List))
			for _, stop := range stopsResp.Data.List {
				ids = append(ids, stop.ID)
			}
			assert.Contains(t, ids, tt.expectedStop,
				"stop names containing %q must be reachable by that query", tt.input)
		})
	}
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

func TestSearchStopsHandlerOrdersByCombinedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()

	registerFixtureCleanup(t, api.GtfsManager.GtfsDB.DB,
		clearIndexedStopAgencies(`'aaa_stop', 'zzz_stop'`),
		`DELETE FROM stop_times WHERE trip_id IN ('trip_ord_alpha', 'trip_ord_zulu')`,
		`DELETE FROM trips WHERE id IN ('trip_ord_alpha', 'trip_ord_zulu')`,
		`DELETE FROM routes WHERE id IN ('route_ord_alpha', 'route_ord_zulu')`,
		`DELETE FROM agencies WHERE id IN ('111', '999')`,
		`DELETE FROM stops WHERE id IN ('aaa_stop', 'zzz_stop')`,
	)

	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('111', 'Agency Low', 'http://low.example.test', 'America/Los_Angeles'),
		       ('999', 'Agency High', 'http://high.example.test', 'America/Los_Angeles')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('service_1', 1, 1, 1, 1, 1, 1, 1, '20240101', '20251231')
	`)
	require.NoError(t, err)

	// Raw stop ID order (aaa_stop, zzz_stop) is the reverse of combined ID order
	// (111_zzz_stop, 999_aaa_stop).
	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, wheelchair_boarding)
		VALUES ('aaa_stop', 'A1', 'Ordertest Alpha', 40.0, -120.0, 0, 1),
		       ('zzz_stop', 'Z1', 'Ordertest Zulu', 40.0, -120.0, 0, 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO routes (id, agency_id, short_name, type)
		VALUES ('route_ord_alpha', '999', 'RT-A', 3), ('route_ord_zulu', '111', 'RT-Z', 3)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('trip_ord_alpha', 'route_ord_alpha', 'service_1'),
		       ('trip_ord_zulu', 'route_ord_zulu', 'service_1')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_ord_alpha', 'aaa_stop', 1, 28800, 28800),
		       ('trip_ord_zulu', 'zzz_stop', 1, 28800, 28800)
	`)
	require.NoError(t, err)

	rebuildStopAgencyIndex(t, api)

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Ordertest"}}))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stopsResp.Data.List, 2)

	assert.Equal(t, []string{"111_zzz_stop", "999_aaa_stop"},
		[]string{stopsResp.Data.List[0].ID, stopsResp.Data.List[1].ID},
		"results must be ordered by combined stop ID")

	// maxCount truncates on that order, so it decides which stop is returned at all.
	respCapped, capped := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Ordertest"}, "maxCount": {"1"}}))
	require.Equal(t, http.StatusOK, respCapped.StatusCode)
	require.Len(t, capped.Data.List, 1)
	assert.Equal(t, "111_zzz_stop", capped.Data.List[0].ID)
	assert.True(t, capped.Data.LimitExceeded)
}

func TestSearchStopsHandlerParentStationReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()

	registerFixtureCleanup(t, api.GtfsManager.GtfsDB.DB,
		clearIndexedStopAgencies(`'child_stop_1', 'parent_stat_1', 'child_stop_2', 'parent_stat_2', 'child_stop_shared', 'child_stop_multi_agency'`),
		`DELETE FROM stop_times WHERE trip_id IN ('trip_parent_test', 'trip_parent_test_2', 'trip_parent_only', 'trip_multi_agency')`,
		`DELETE FROM trips WHERE id IN ('trip_parent_test', 'trip_parent_test_2', 'trip_parent_only', 'trip_multi_agency')`,
		`DELETE FROM routes WHERE id IN ('route_parent_test', 'route_parent_test_2', 'route_parent_only')`,
		`DELETE FROM agencies WHERE id IN ('999', '888')`,
		`DELETE FROM stops WHERE id IN ('child_stop_1', 'parent_stat_1', 'child_stop_2', 'parent_stat_2', 'child_stop_shared', 'child_stop_multi_agency')`,
	)

	// routes.agency_id and trips.service_id are foreign keys, so the agencies and the
	// calendar entry the fixture rows point at must exist before those rows are inserted.
	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('999', 'Parent Test Agency One', 'http://one.example.test', 'America/Los_Angeles'),
		       ('888', 'Parent Test Agency Two', 'http://two.example.test', 'America/Los_Angeles')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('service_1', 1, 1, 1, 1, 1, 1, 1, '20240101', '20251231')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
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

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, wheelchair_boarding)
		VALUES ('parent_stat_2', 'P2', 'Parent Station Two', 40.0, -120.0, 1, 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, parent_station, wheelchair_boarding)
		VALUES ('child_stop_2', 'C2', 'Child Stop Two', 40.0, -120.0, 0, 'parent_stat_2', 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO routes (id, agency_id, short_name, type)
		VALUES ('route_parent_test_2', '888', 'RT-P2', 3)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('trip_parent_test_2', 'route_parent_test_2', 'service_1')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_parent_test_2', 'child_stop_2', 1, 28800, 28800)
	`)
	require.NoError(t, err)

	// A second agency serving parent_stat_1, so that one parent station is referenced
	// under two different agency prefixes.
	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, parent_station, wheelchair_boarding)
		VALUES ('child_stop_shared', 'C4', 'Child Stop Shared', 40.0, -120.0, 0, 'parent_stat_1', 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_parent_test_2', 'child_stop_shared', 2, 29400, 29400)
	`)
	require.NoError(t, err)

	// parent_stat_1 is served directly by its own route, with no child stop on that
	// route, so references.routes must be populated from the parent lookup alone.
	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO routes (id, agency_id, short_name, type)
		VALUES ('route_parent_only', '999', 'RT-PARENT-ONLY', 3)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('trip_parent_only', 'route_parent_only', 'service_1')
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_parent_only', 'parent_stat_1', 1, 28800, 28800)
	`)
	require.NoError(t, err)

	// A stop served by routes from both agencies, to verify agency selection is
	// deterministic (lowest agency ID) rather than dependent on query row order.
	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, name, lat, lon, location_type, parent_station, wheelchair_boarding)
		VALUES ('child_stop_multi_agency', 'C5', 'Child Stop Multi Agency', 40.0, -120.0, 0, 'parent_stat_1', 1)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_parent_test', 'child_stop_multi_agency', 2, 28800, 28800)
	`)
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('trip_parent_test_2', 'child_stop_multi_agency', 3, 28800, 28800)
	`)
	require.NoError(t, err)

	rebuildStopAgencyIndex(t, api)

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Child Stop"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, stopsResp.Code)

	require.Len(t, stopsResp.Data.List, 4)

	stopsByName := make(map[string]models.Stop, len(stopsResp.Data.List))
	seenIDs := make(map[string]bool, len(stopsResp.Data.List))
	for _, stop := range stopsResp.Data.List {
		stopsByName[stop.Name] = stop
		// child_stop_multi_agency is served by both 888 and 999: stop_agencies holds one
		// row per (stop, agency) pair, so joining it without collapsing to a single agency
		// would return this stop twice.
		assert.False(t, seenIDs[stop.ID], "stop %q appeared more than once in the result list", stop.ID)
		seenIDs[stop.ID] = true
	}

	child1, ok := stopsByName["Child Stop One"]
	require.True(t, ok)
	assert.Equal(t, "999_child_stop_1", child1.ID)
	assert.Equal(t, "999_parent_stat_1", child1.Parent)

	child2, ok := stopsByName["Child Stop Two"]
	require.True(t, ok)
	assert.Equal(t, "888_child_stop_2", child2.ID)
	assert.Equal(t, "888_parent_stat_2", child2.Parent)

	// The same parent station reached through a second agency keeps that agency's prefix.
	childShared, ok := stopsByName["Child Stop Shared"]
	require.True(t, ok)
	assert.Equal(t, "888_child_stop_shared", childShared.ID)
	assert.Equal(t, "888_parent_stat_1", childShared.Parent)

	// A stop served by two agencies picks the lowest agency ID deterministically,
	// rather than whichever route GetRoutesForStops happens to return first.
	childMultiAgency, ok := stopsByName["Child Stop Multi Agency"]
	require.True(t, ok)
	assert.Equal(t, "888_child_stop_multi_agency", childMultiAgency.ID)
	assert.Equal(t, "888_parent_stat_1", childMultiAgency.Parent)
	assert.Equal(t, []string{"888_route_parent_test_2", "999_route_parent_test"}, childMultiAgency.RouteIDs,
		"routeIds must be in a stable, deterministic order")

	referenceIDs := make([]string, 0, len(stopsResp.Data.References.Stops))
	for _, parentRef := range stopsResp.Data.References.Stops {
		referenceIDs = append(referenceIDs, parentRef.ID)
	}
	require.Equal(t, []string{"888_parent_stat_1", "888_parent_stat_2", "999_parent_stat_1"}, referenceIDs,
		"references.stops must hold one entry per (parent station, agency) pair, sorted by ID")

	parentRefsByID := make(map[string]models.Stop, len(stopsResp.Data.References.Stops))
	for _, parentRef := range stopsResp.Data.References.Stops {
		parentRefsByID[parentRef.ID] = parentRef
	}

	// The contract that matters: every parent a returned stop points at must resolve.
	for _, stop := range stopsResp.Data.List {
		if stop.Parent == "" {
			continue
		}
		assert.Contains(t, parentRefsByID, stop.Parent,
			"parent %q of stop %q has no entry in references.stops", stop.Parent, stop.ID)
	}

	assert.Equal(t, "Parent Station One", parentRefsByID["999_parent_stat_1"].Name)
	assert.Equal(t, "Parent Station One", parentRefsByID["888_parent_stat_1"].Name)
	assert.Equal(t, "Parent Station Two", parentRefsByID["888_parent_stat_2"].Name)

	// Parent references carry route arrays like every other stop-emitting endpoint.
	for _, parentRef := range stopsResp.Data.References.Stops {
		assert.NotNil(t, parentRef.RouteIDs, "parent %q is missing routeIds", parentRef.ID)
		assert.NotNil(t, parentRef.StaticRouteIDs, "parent %q is missing staticRouteIds", parentRef.ID)
	}

	// route_parent_only serves parent_stat_1 directly and is not on any returned child
	// stop, so it must still be merged into references.routes rather than dropped.
	routesByID := make(map[string]models.Route, len(stopsResp.Data.References.Routes))
	for _, route := range stopsResp.Data.References.Routes {
		routesByID[route.ID] = route
	}
	_, hasParentOnlyRoute := routesByID["999_route_parent_only"]
	assert.True(t, hasParentOnlyRoute,
		"references.routes must include parent-only routes so parent routeIds always resolve")
	assert.Equal(t, "999", routesByID["999_route_parent_only"].AgencyID,
		"a route's agencyId must match its own ID prefix, not the child stop's agency that reached it")

	// route_parent_only belongs to agency 999. parent_stat_1 is also reached through
	// agency 888 (via child_stop_shared/child_stop_multi_agency), so a route must not be
	// re-prefixed with the agency of whichever child stop happened to reach the parent.
	_, hasMisprefixedRoute := routesByID["888_route_parent_only"]
	assert.False(t, hasMisprefixedRoute,
		"routes must not be re-prefixed with a child stop's agency")

	// A parent reached through agency 888 still points at the route's real 999-prefixed ID.
	assert.Contains(t, parentRefsByID["888_parent_stat_1"].RouteIDs, "999_route_parent_only")

	// Every routeId referenced by any parent stop must resolve in references.routes.
	for _, parentRef := range stopsResp.Data.References.Stops {
		for _, routeID := range parentRef.RouteIDs {
			assert.Contains(t, routesByID, routeID,
				"parent %q references route %q which is missing from references.routes", parentRef.ID, routeID)
		}
	}
}

func TestSearchStopsHandlerRouteTypeExclusion(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	db := api.GtfsManager.GtfsDB.DB

	// The RABA agency and the service_1 calendar are shared records and are left in place.
	registerFixtureCleanup(t, db,
		clearIndexedStopAgencies(`'zero_route_stop', 'school_bus_stop', 'valid_bus_stop', 'two_school_routes_stop', 'limit_ghost_1', 'limit_ghost_2', 'limit_valid_3'`),
		`DELETE FROM stop_times WHERE trip_id IN ('school_trip_1', 'school_trip_2', 'valid_trip_1')`,
		`DELETE FROM trips WHERE id IN ('school_trip_1', 'school_trip_2', 'valid_trip_1')`,
		`DELETE FROM routes WHERE id IN ('school_route_1', 'school_route_2', 'valid_route_1')`,
		`DELETE FROM stops WHERE id IN ('zero_route_stop', 'school_bus_stop', 'valid_bus_stop', 'two_school_routes_stop', 'limit_ghost_1', 'limit_ghost_2', 'limit_valid_3')`,
	)

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

	rebuildStopAgencyIndex(t, api)

	// Test 0 routes exclusion
	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Ghost"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List, "Expected Ghost Stop to be excluded (0 routes)")

	// Test School bus exclusion
	resp, stopsResp = callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Single Special"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stopsResp.Data.List, "Expected Single Special Stop to be excluded (single route type 712)")
	assert.Empty(t, stopsResp.Data.References.Routes, "Expected excluded stop's routes to not leak into references")
	assert.Empty(t, stopsResp.Data.References.Agencies, "Expected excluded stop's agencies to not leak into references")

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
	// The two zero-route ghosts resolve no agency, so they have no combined ID to sort by
	// and fall after limit_valid_3. It takes the first of the two cap slots; the ghost
	// filling the second is then dropped by the zero-route filter.
	require.Len(t, stopsResp.Data.List, 1, "Expected only the routed stop to survive filtering")
	assert.True(t, strings.HasSuffix(stopsResp.Data.List[0].ID, "limit_valid_3"))
}

func TestSearchStopsHandlerParentStationCrossAgencyReference(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	db := api.GtfsManager.GtfsDB.DB

	registerFixtureCleanup(t, db,
		clearIndexedStopAgencies(`'child_stop_cross', 'parent_station_cross'`),
		`DELETE FROM stop_times WHERE trip_id IN ('child_trip', 'parent_trip')`,
		`DELETE FROM trips WHERE id IN ('child_trip', 'parent_trip')`,
		`DELETE FROM routes WHERE id IN ('child_route', 'parent_route')`,
		`DELETE FROM stops WHERE id IN ('child_stop_cross', 'parent_station_cross')`,
		`DELETE FROM agencies WHERE id IN ('888', '999')`,
	)

	// Insert mock data for cross-agency verification
	_, err := db.Exec(`
		INSERT INTO agencies (id, name, url, timezone) VALUES ('888', 'Agency 888', 'http://agency888.com', 'America/Los_Angeles');
		INSERT INTO agencies (id, name, url, timezone) VALUES ('999', 'Agency 999', 'http://agency999.com', 'America/Los_Angeles');

		-- Parent station
		INSERT INTO stops (id, name, lat, lon, location_type)
		VALUES ('parent_station_cross', 'Parent Station Cross Agency', 40.0, -120.0, 1);

		-- Child stop belonging to Agency 888, with parent station
		INSERT INTO stops (id, name, lat, lon, location_type, parent_station)
		VALUES ('child_stop_cross', 'Child Stop Cross Agency', 40.0, -120.0, 0, 'parent_station_cross');

		-- Route serving child stop (Agency 888)
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('child_route', '888', 'Child Route', 3);
		INSERT OR IGNORE INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('service_1', 1, 1, 1, 1, 1, 1, 1, '20230101', '20251231');
		INSERT INTO trips (id, route_id, service_id) VALUES ('child_trip', 'child_route', 'service_1');
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('child_trip', 'child_stop_cross', 1, 28800, 28800);

		-- Route serving parent station (Agency 999)
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('parent_route', '999', 'Parent Route', 3);
		INSERT INTO trips (id, route_id, service_id) VALUES ('parent_trip', 'parent_route', 'service_1');
		INSERT INTO stop_times (trip_id, stop_id, stop_sequence, arrival_time, departure_time)
		VALUES ('parent_trip', 'parent_station_cross', 1, 29000, 29000);
	`)
	require.NoError(t, err)

	rebuildStopAgencyIndex(t, api)

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Child Stop Cross"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stopsResp.Data.List, 1)

	// Verify references.routes contains the parent route (999_parent_route)
	var foundParentRoute bool
	for _, route := range stopsResp.Data.References.Routes {
		if route.ID == "999_parent_route" {
			foundParentRoute = true
			break
		}
	}
	assert.True(t, foundParentRoute, "Expected 999_parent_route in references.routes")

	// Verify references.agencies contains Agency 999 (the parent route's agency)
	var foundParentAgency bool
	for _, agency := range stopsResp.Data.References.Agencies {
		if agency.ID == "999" {
			foundParentAgency = true
			break
		}
	}
	assert.True(t, foundParentAgency, "Expected Agency 999 in references.agencies")
}
