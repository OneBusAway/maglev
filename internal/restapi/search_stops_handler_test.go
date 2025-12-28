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

	// Manually set up server to handle custom 400 response format
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Call without 'input' parameter
	resp, err := http.Get(server.URL + "/api/where/search/stop.json?key=TEST")
	require.NoError(t, err)
	defer resp.Body.Close()

	// 1. Assert Status Code matches 400
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// 2. Decode specific validation error structure
	var errorResponse struct {
		FieldErrors map[string][]string `json:"fieldErrors"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err, "Should be able to decode validation error response")

	// 3. Verify content
	assert.Contains(t, errorResponse.FieldErrors, "input", "Should contain error for 'input' field")
}

func TestSearchStopsHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	// Get a real stop from the loaded test data
	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops, "Test data should contain at least one stop")
	targetStop := stops[0]

	// URL Encode the input parameter to handle spaces (e.g. "Downtown Passenger Terminal")
	query := url.QueryEscape(targetStop.Name)
	reqUrl := fmt.Sprintf("/api/where/search/stop.json?key=TEST&input=%s", query)

	// Manually perform request to inspect errors clearly
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + reqUrl)
	require.NoError(t, err)
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "Response status should be 200 OK. Body: %s", string(bodyBytes))

	var model models.ResponseModel
	err = json.Unmarshal(bodyBytes, &model)
	require.NoError(t, err, "Failed to decode response JSON: %s", string(bodyBytes))

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")
	assert.NotEmpty(t, data)

	assert.Equal(t, false, data["limitExceeded"])
	assert.Equal(t, false, data["outOfRange"])

	list, ok := data["list"].([]interface{})
	require.True(t, ok, "List should exist and be an array")
	assert.NotEmpty(t, list, "Search should return at least the stop we queried for")

	firstResult, ok := list[0].(map[string]interface{})
	require.True(t, ok)

	assert.NotEmpty(t, firstResult["id"], "Stop ID should not be empty")
	assert.NotEmpty(t, firstResult["name"], "Stop Name should not be empty")
	assert.NotEmpty(t, firstResult["lat"], "Lat should not be empty")
	assert.NotEmpty(t, firstResult["lon"], "Lon should not be empty")

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "References section should exist")

	agenciesRef, ok := references["agencies"].([]interface{})
	assert.True(t, ok, "References should contain agencies array")
	assert.NotEmpty(t, agenciesRef, "References should contain at least one agency")
}

func TestSearchStopsHandlerNoResults(t *testing.T) {
	api := createTestApi(t)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/search/stop.json?key=TEST&input=NonExistentStopName12345")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, list, "List should be empty for no results")
}

func TestSearchStopsHandlerMaxCount(t *testing.T) {
	api := createTestApi(t)

	stops := api.GtfsManager.GetStops()
	if len(stops) < 2 {
		t.Skip("Not enough stops in test data to verify maxCount limiting")
	}

	targetStop := stops[0]
	// URL Encode the input parameter
	query := url.QueryEscape(targetStop.Name)
	
	reqUrl := fmt.Sprintf("/api/where/search/stop.json?key=TEST&input=%s&maxCount=1", query)

	// Use manual setup to ensure URL is handled correctly
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + reqUrl)
	require.NoError(t, err)
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "Response status should be 200 OK. Body: %s", string(bodyBytes))

	var model models.ResponseModel
	err = json.Unmarshal(bodyBytes, &model)
	require.NoError(t, err)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)

	assert.LessOrEqual(t, len(list), 1, "Should return at most 1 result")
}