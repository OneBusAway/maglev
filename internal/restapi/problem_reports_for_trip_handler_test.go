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

const testTripForProblemReports = "1_12345"

func problemReportsForTripURL(tripID string, params ...url.Values) string {
	q := url.Values{"key": {"PROTECTED-TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/problem-reports-for-trip/" + tripID + ".json?" + q.Encode()
}

func TestProblemReportsForTripRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ProblemReportsForTripResponse](t, api,
		"/api/where/problem-reports-for-trip/"+testTripForProblemReports+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestProblemReportsForTripEmptyList(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	resp, model := callAPIHandler[ProblemReportsForTripResponse](t, api, problemReportsForTripURL(testTripForProblemReports))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Empty(t, model.Data.List, "List should be empty when no reports exist")
	assert.False(t, model.Data.LimitExceeded)
}

func TestProblemReportsForTripSubmitThenRetrieve(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	submitURL := fmt.Sprintf("/api/where/report-problem-with-trip/%s.json?key=TEST&code=vehicle_never_came&userComment=Test+report&userLat=47.6097&userLon=-122.3331", testTripForProblemReports)
	submitResp, _ := callAPIHandler[models.ResponseModel](t, api, submitURL)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)

	getURLUnauth := "/api/where/problem-reports-for-trip/" + testTripForProblemReports + ".json?key=TEST"
	unauthResp, unauthModel := callAPIHandler[ProblemReportsForTripResponse](t, api, getURLUnauth)
	assert.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, unauthModel.Code)

	resp, model := callAPIHandler[ProblemReportsForTripResponse](t, api, problemReportsForTripURL(testTripForProblemReports))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	require.Len(t, model.Data.List, 1, "Should have exactly one report")

	report := model.Data.List[0]
	assert.Equal(t, "12345", report.TripID)
	assert.Equal(t, "vehicle_never_came", report.Code)
	assert.Equal(t, "Test report", report.UserComment)
	require.NotNil(t, report.UserLat)
	require.NotNil(t, report.UserLon)
	assert.InDelta(t, 47.6097, *report.UserLat, 0.001)
	assert.InDelta(t, -122.3331, *report.UserLon, 0.001)
	assert.Nil(t, report.UserLocationAccuracy)
}

func TestProblemReportsForTripZeroCoordinates(t *testing.T) {
	api := createTestApi(t)
	api.Config.ProtectedApiKeys = []string{"PROTECTED-TEST"}
	defer api.Shutdown()

	submitURL := fmt.Sprintf("/api/where/report-problem-with-trip/%s.json?key=TEST&code=other&userLat=0&userLon=0&userLocationAccuracy=0", testTripForProblemReports)
	submitResp, _ := callAPIHandler[models.ResponseModel](t, api, submitURL)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)

	resp, model := callAPIHandler[ProblemReportsForTripResponse](t, api, problemReportsForTripURL(testTripForProblemReports))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Find the zero-coordinate report (others from prior tests may also be present).
	var zeroReport *models.ProblemReportTrip
	for i := range model.Data.List {
		r := &model.Data.List[i]
		if r.UserLat != nil && *r.UserLat == 0.0 && r.UserLon != nil && *r.UserLon == 0.0 {
			zeroReport = r
			break
		}
	}
	require.NotNil(t, zeroReport, "should find a report with userLat=0, userLon=0")
	require.NotNil(t, zeroReport.UserLat, "userLat=0 must be present, not omitted")
	require.NotNil(t, zeroReport.UserLon, "userLon=0 must be present, not omitted")
	require.NotNil(t, zeroReport.UserLocationAccuracy, "userLocationAccuracy=0 must be present, not omitted")
	assert.InDelta(t, 0.0, *zeroReport.UserLocationAccuracy, 0.001)
}
