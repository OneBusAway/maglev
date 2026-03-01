package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProblemReportsForTripRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/problem-reports-for-trip/12345.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestProblemReportsForTrip_EmptyList(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	tripID := "1_12345"
	url := fmt.Sprintf("/api/where/problem-reports-for-trip/%s.json?key=PROTECTED-TEST", tripID)

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

func TestProblemReportsForTrip_SubmitThenRetrieve(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	tripID := "1_12345"

	// First, submit a problem report
	submitURL := fmt.Sprintf("/api/where/report-problem-with-trip/%s.json?key=TEST&code=vehicle_never_came&userComment=Test+report&userLat=47.6097&userLon=-122.3331", tripID)
	submitResp, submitModel := serveApiAndRetrieveEndpoint(t, api, submitURL)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)
	require.Equal(t, 200, submitModel.Code)

	// retrieve with standard key (should fail)
	getURLUnauth := fmt.Sprintf("/api/where/problem-reports-for-trip/%s.json?key=TEST", tripID)
	unauthResp, unauthModel := serveApiAndRetrieveEndpoint(t, api, getURLUnauth)
	assert.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, unauthModel.Code)

	// retrieve reports for this trip with protected key
	getURL := fmt.Sprintf("/api/where/problem-reports-for-trip/%s.json?key=PROTECTED-TEST", tripID)
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
	assert.Equal(t, "12345", report["tripId"])
	assert.Equal(t, "vehicle_never_came", report["code"])
	assert.Equal(t, "Test report", report["userComment"])
	userLat, ok := report["userLat"].(float64)
	require.True(t, ok, "userLat should be a float64")
	assert.InDelta(t, 47.6097, userLat, 0.001)

	userLon, ok := report["userLon"].(float64)
	require.True(t, ok, "userLon should be a float64")
	assert.InDelta(t, -122.3331, userLon, 0.001)
}
