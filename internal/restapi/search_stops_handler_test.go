package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	resp, stopsResp := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"   "}}))

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
