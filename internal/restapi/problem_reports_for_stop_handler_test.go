package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProblemReportsForStopRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/problem-reports-for-stop/12345.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "Unauthorized: Valid protected API key required", model.Text)
}

func TestProblemReportsForStop_EmptyList(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	stopID := "1_75403"
	url := fmt.Sprintf("/api/where/problem-reports-for-stop/%s.json?key=PROTECTED-TEST", stopID)

	resp, model := serveApiAndRetrieveEndpoint(t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	list, ok := data["list"].([]interface{})
	require.True(t, ok, "Data should contain a list")
	assert.Empty(t, list, "List should be empty when no reports exist")

	assert.Equal(t, false, data["limitExceeded"])
}

func TestProblemReportsForStop_SubmitThenRetrieve(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	stopID := "1_75403"

	// First, submit a problem report with standard key
	submitURL := fmt.Sprintf("/api/where/report-problem-with-stop/%s.json?key=TEST&code=stop_name_wrong&userComment=Wrong+name&userLat=38.5678&userLon=-121.4321", stopID)
	submitResp, submitModel := serveApiAndRetrieveEndpoint(t, api, submitURL)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)
	require.Equal(t, 200, submitModel.Code)

	// retrieve with standard key (should fail)
	getURLUnauth := fmt.Sprintf("/api/where/problem-reports-for-stop/%s.json?key=TEST", stopID)
	unauthResp, unauthModel := serveApiAndRetrieveEndpoint(t, api, getURLUnauth)
	assert.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, unauthModel.Code)

	// retrieve reports for this stop with protected key
	getURL := fmt.Sprintf("/api/where/problem-reports-for-stop/%s.json?key=PROTECTED-TEST", stopID)
	resp, model := serveApiAndRetrieveEndpoint(t, api, getURL)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	list, ok := data["list"].([]interface{})
	require.True(t, ok, "Data should contain a list")
	require.Len(t, list, 1, "Should have exactly one report")

	// Verify the report contents
	report, ok := list[0].(map[string]interface{})
	require.True(t, ok, "Report should be a map")
	assert.Equal(t, "75403", report["stopId"])
	assert.Equal(t, "stop_name_wrong", report["code"])
	assert.Equal(t, "Wrong name", report["userComment"])

	userLat, ok := report["userLat"].(float64)
	require.True(t, ok, "userLat should be a float64")
	assert.InDelta(t, 38.5678, userLat, 0.001)

	userLon, ok := report["userLon"].(float64)
	require.True(t, ok, "userLon should be a float64")
	assert.InDelta(t, -121.4321, userLon, 0.001)
}
