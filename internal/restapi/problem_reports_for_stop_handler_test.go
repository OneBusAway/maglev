package restapi

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

const testStopForProblemReports = "1_75403"

func problemReportsForStopURL(stopID string, params ...url.Values) string {
	q := url.Values{"key": {"PROTECTED-TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/problem-reports-for-stop/" + stopID + ".json?" + q.Encode()
}

func TestProblemReportsForStopRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ProblemReportsForStopResponse](t, api,
		"/api/where/problem-reports-for-stop/"+testStopForProblemReports+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestProblemReportsForStopEmptyList(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	resp, model := callAPIHandler[ProblemReportsForStopResponse](t, api, problemReportsForStopURL(testStopForProblemReports))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Empty(t, model.Data.List, "List should be empty when no reports exist")
	assert.False(t, model.Data.LimitExceeded)
}

func TestProblemReportsForStopSubmitThenRetrieve(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	submitURL := fmt.Sprintf("/api/where/report-problem-with-stop/%s.json?key=TEST&code=stop_name_wrong&userComment=Wrong+name&userLat=38.5678&userLon=-121.4321", testStopForProblemReports)
	submitResp, _ := callAPIHandler[models.ResponseModel](t, api, submitURL)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)

	getURLUnauth := "/api/where/problem-reports-for-stop/" + testStopForProblemReports + ".json?key=TEST"
	unauthResp, unauthModel := callAPIHandler[ProblemReportsForStopResponse](t, api, getURLUnauth)
	assert.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, unauthModel.Code)

	resp, model := callAPIHandler[ProblemReportsForStopResponse](t, api, problemReportsForStopURL(testStopForProblemReports))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	require.Len(t, model.Data.List, 1, "Should have exactly one report")

	report := model.Data.List[0]
	assert.Equal(t, "75403", report.StopID)
	assert.Equal(t, "stop_name_wrong", report.Code)
	assert.Equal(t, "Wrong name", report.UserComment)
	assert.InDelta(t, 38.5678, report.UserLat, 0.001)
	assert.InDelta(t, -121.4321, report.UserLon, 0.001)
}
