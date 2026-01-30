package restapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

func TestSearchStopsHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	// Try without key
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/search/stop.json?input=test")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)

	// Try with invalid key
	resp, _ = serveApiAndRetrieveEndpoint(t, api, "/api/where/search/stop.json?input=test&key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSearchStopsHandlerMissingInput(t *testing.T) {
	api := createTestApi(t)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/search/stop.json?key=TEST")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResponse struct {
		Code int `json:"code"`
		Data struct {
			FieldErrors map[string][]string `json:"fieldErrors"`
		} `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	assert.Contains(t, errorResponse.Data.FieldErrors, "input")
}

func TestSearchStopsHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops)
	targetStop := stops[0]

	query := url.QueryEscape(targetStop.Name)
	reqUrl := fmt.Sprintf("/api/where/search/stop.json?key=TEST&input=%s", query)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + reqUrl)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(bodyBytes))

	var model models.ResponseModel
	err = json.Unmarshal(bodyBytes, &model)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, false, data["limitExceeded"])
	assert.Equal(t, false, data["outOfRange"])

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, list)

	firstResult, ok := list[0].(map[string]interface{})
	require.True(t, ok)

	assert.NotEmpty(t, firstResult["id"])
	assert.NotEmpty(t, firstResult["name"])
	assert.NotEmpty(t, firstResult["lat"])
	assert.NotEmpty(t, firstResult["lon"])

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	agenciesRef, ok := references["agencies"].([]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, agenciesRef)
}

func TestSearchStopsHandlerNoResults(t *testing.T) {
	api := createTestApi(t)

	resp, model := serveApiAndRetrieveEndpoint(
		t,
		api,
		"/api/where/search/stop.json?key=TEST&input=NonExistentStopName12345",
	)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, list)
}

func TestSearchStopsHandlerMaxCount(t *testing.T) {
	api := createTestApi(t)

	stops := api.GtfsManager.GetStops()
	if len(stops) < 2 {
		t.Skip("Not enough stops")
	}

	targetStop := stops[0]
	query := url.QueryEscape(targetStop.Name)

	reqUrl := fmt.Sprintf(
		"/api/where/search/stop.json?key=TEST&input=%s&maxCount=1",
		query,
	)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + reqUrl)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(bodyBytes))

	var model models.ResponseModel
	err = json.Unmarshal(bodyBytes, &model)
	require.NoError(t, err)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)

	assert.LessOrEqual(t, len(list), 1)
}

func TestSearchStopsHandlerWhitespaceOnlyInput(t *testing.T) {
	api := createTestApi(t)

	resp, model := serveApiAndRetrieveEndpoint(
		t,
		api,
		"/api/where/search/stop.json?key=TEST&input=%20%20%20",
	)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)

	assert.Empty(t, list)
}

func TestSearchStopsHandlerSpecialCharactersOnly(t *testing.T) {
	api := createTestApi(t)

	query := url.QueryEscape(`*()"`)
	resp, model := serveApiAndRetrieveEndpoint(
		t,
		api,
		"/api/where/search/stop.json?key=TEST&input="+query,
	)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)

	assert.Empty(t, list)
}

func TestSearchStopsHandlerMaxCountBoundaries(t *testing.T) {
	api := createTestApi(t)

	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops)

	targetStop := stops[0]
	query := url.QueryEscape(targetStop.Name)

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
			reqUrl := fmt.Sprintf(
				"/api/where/search/stop.json?key=TEST&input=%s&maxCount=%s",
				query,
				tt.maxCount,
			)

			resp, model := serveApiAndRetrieveEndpoint(t, api, reqUrl)

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			data, ok := model.Data.(map[string]interface{})
			require.True(t, ok)

			list, ok := data["list"].([]interface{})
			require.True(t, ok)

			assert.NotEmpty(t, list)
		})
	}
}

func TestSearchStopsHandlerFTSInjectionAttempt(t *testing.T) {
	api := createTestApi(t)

	query := url.QueryEscape(`test" OR "1"="1`)
	resp, model := serveApiAndRetrieveEndpoint(
		t,
		api,
		"/api/where/search/stop.json?key=TEST&input="+query,
	)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)

	assert.Less(t, len(list), 50)
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "     ",
			expected: "",
		},
		{
			name:     "all special characters",
			input:    `"*()`,
			expected: "",
		},
		{
			name:     "mixed case operators AND",
			input:    "test AND foo",
			expected: "test foo",
		},
		{
			name:     "mixed case operators And",
			input:    "test And foo",
			expected: "test foo",
		},
		{
			name:     "mixed case operators aNd",
			input:    "test aNd foo",
			expected: "test foo",
		},
		{
			name:     "consecutive operators",
			input:    "foo AND AND bar",
			expected: "foo bar",
		},
		{
			name:     "operator at beginning",
			input:    "AND test",
			expected: "test",
		},
		{
			name:     "operator at end",
			input:    "test OR",
			expected: "test",
		},
		{
			name:     "unicode input",
			input:    "中央駅 テスト",
			expected: "中央駅 テスト",
		},
		{
			name:     "colon character",
			input:    "column:value",
			expected: "column value",
		},
		{
			name:     "caret character",
			input:    "test^2",
			expected: "test 2",
		},
		{
			name:     "curly braces",
			input:    "test{foo}bar",
			expected: "test foo bar",
		},
		{
			name:     "square brackets",
			input:    "test[foo]bar",
			expected: "test foo bar",
		},
		{
			name:     "angle brackets",
			input:    "test<foo>bar",
			expected: "test foo bar",
		},
		{
			name:     "tilde character",
			input:    "test~2",
			expected: "test 2",
		},
		{
			name:     "pipe character",
			input:    "test|foo",
			expected: "test foo",
		},
		{
			name:     "NEAR operator",
			input:    "test NEAR foo",
			expected: "test foo",
		},
		{
			name:     "NEAR operator mixed case",
			input:    "test near foo",
			expected: "test foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := sanitizeFTS5Query(tt.input)
			assert.Equal(t, tt.expected, out)
		})
	}
}
