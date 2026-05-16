package restapi

import (
	"maps"
	"net/http"
	"net/url"
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

	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/search/stop.json?input=test")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)

	resp, _ = callAPIHandler[StopsResponse](t, api, "/api/where/search/stop.json?input=test&key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSearchStopsHandlerMissingInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/search/stop.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, model.Data.FieldErrors, "input")
}

func TestSearchStopsHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	require.NotEmpty(t, model.Data.List)
	assert.False(t, model.Data.LimitExceeded)
	assert.False(t, model.Data.OutOfRange)

	for _, stop := range model.Data.List {
		assert.NotEmpty(t, stop.ID)
		assert.NotEmpty(t, stop.Name)
		assert.NotZero(t, stop.Lat)
		assert.NotZero(t, stop.Lon)
	}

	assert.NotEmpty(t, model.Data.References.Agencies)
}

func TestSearchStopsHandlerNoResults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"NonExistentStopName12345"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.List)
}

func TestSearchStopsHandlerMaxCount(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}, "maxCount": {"1"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.LessOrEqual(t, len(model.Data.List), 1)
}

func TestSearchStopsHandlerWhitespaceOnlyInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"   "}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.List)
}

func TestSearchStopsHandlerSpecialCharactersOnly(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {`*()"`}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.List)
}

func TestSearchStopsHandlerMaxCountBoundaries(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name     string
		maxCount string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"tooLarge", "101"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {"Buenaventura"}, "maxCount": {tt.maxCount}}))

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.NotEmpty(t, model.Data.List)
		})
	}
}

func TestSearchStopsHandlerFTSInjectionAttempt(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsResponse](t, api, searchStopsURL(url.Values{"input": {`test" OR "1"="1`}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Less(t, len(model.Data.List), 50)
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
