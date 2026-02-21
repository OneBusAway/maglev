package restapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportProblemWithStopRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	payload := ReportProblemStop{CompositeID: "12345.json"}
	body, _ := json.Marshal(payload)

	resp, model := serveApiAndRetrieveEndpointWithBody(
		t,
		api,
		http.MethodPost,
		"/api/where/report-problem-with-stop?key=invalid",
		body,
	)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithStopEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	lat, lon, acc := 47.6097, -122.3331, 10.0
	stopId := "1_75403.json"

	payload := ReportProblemStop{
		CompositeID:          stopId,
		Code:                 "stop_name_wrong",
		UserComment:          "Test comment",
		UserLat:              &lat,
		UserLon:              &lon,
		UserLocationAccuracy: &acc,
	}

	body, _ := json.Marshal(payload)
	resp, model := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-stop?key=TEST", body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Empty(t, data)

	payload.CompositeID = ""
	bodyEmpty, _ := json.Marshal(payload)
	respErr, modelErr := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-stop?key=TEST", bodyEmpty)

	assert.Equal(t, http.StatusBadRequest, respErr.StatusCode)
	assert.Equal(t, "id cannot be empty", modelErr.Text)
}

func TestReportProblemWithStop_MinimalParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	payload := ReportProblemStop{
		CompositeID: "1_12345.json",
	}

	body, _ := json.Marshal(payload)
	resp, model := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-stop?key=TEST", body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 200, model.Code)
}

func TestReportProblemWithStopSanitization(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	invalidJSON := []byte(`{"composite_id": "1_75403.json", "user_lat": "invalid"}`)
	resp, _ := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-stop?key=TEST", invalidJSON)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Should return 400 for invalid types in JSON")

	longComment := strings.Repeat("a", 1000)
	payload := ReportProblemStop{
		CompositeID: "1_12345.json",
		UserComment: longComment,
	}

	body, _ := json.Marshal(payload)
	respLong, modelLong := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-stop?key=TEST", body)

	assert.Equal(t, http.StatusOK, respLong.StatusCode)
	assert.Equal(t, 200, modelLong.Code)
}
