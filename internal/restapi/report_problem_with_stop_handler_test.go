package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportProblemWithStopRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/report-problem-with-stop/12345.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithStopEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopId := "1_75403"

	url := fmt.Sprintf("/api/where/report-problem-with-stop/%s.json?key=TEST&code=stop_name_wrong&userComment=Test+comment&userLat=47.6097&userLon=-122.3331&userLocationAccuracy=10", stopId)

	resp, model := serveApiAndRetrieveEndpoint(t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	assert.Empty(t, data, "Data should be an empty object")

	nullURL := "/api/where/report-problem-with-stop/.json?key=TEST&code=stop_name_wrong"
	nullResp, nullModel := serveApiAndRetrieveEndpoint(t, api, nullURL)

	assert.Equal(t, http.StatusOK, nullResp.StatusCode)
	assert.Equal(t, 0, nullModel.Code)
	assert.Nil(t, nullModel.Data, "Response data should be null when stop ID is missing")
}
