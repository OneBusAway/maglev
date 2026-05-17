package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportProblemWithStopRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/12345.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithStopEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := "1_75403"

	resp, model := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/"+stopID+".json?key=TEST&code=stop_name_wrong&userComment=Test+comment&userLat=47.6097&userLon=-122.3331&userLocationAccuracy=10")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	nullResp, nullModel := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/.json?key=TEST&code=stop_name_wrong")

	assert.Equal(t, http.StatusBadRequest, nullResp.StatusCode, "Should return 400 when ID is missing")
	assert.Equal(t, http.StatusBadRequest, nullModel.Code)
	assert.Equal(t, "id cannot be empty", nullModel.Text)
}

func TestReportProblemWithStopMinimalParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/1_75403.json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
}

func TestReportProblemWithStopSanitization(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopID := "1_75403"

	resp, model := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/"+stopID+".json?key=TEST&code=stop_name_wrong&userLat=invalid&userLon=not_a_number")

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should handle invalid userLat/userLon gracefully without 500 error")
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	longComment := strings.Repeat("a", 1000)
	respLong, modelLong := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-stop/"+stopID+".json?key=TEST&code=stop_name_wrong&userComment="+longComment)

	assert.Equal(t, http.StatusOK, respLong.StatusCode, "Should handle massive user comments gracefully")
	assert.Equal(t, http.StatusOK, modelLong.Code)
}
