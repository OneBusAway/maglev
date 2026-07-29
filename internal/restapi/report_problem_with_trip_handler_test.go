package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reportProblemWithTripURL(tripID string, params ...url.Values) string {
	q := url.Values{"key": {"TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/report-problem-with-trip/" + tripID + ".json?" + q.Encode()
}

func TestReportProblemWithTripRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api,
		"/api/where/report-problem-with-trip/12345.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithTripEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_12345", url.Values{
		"serviceDate":          {"1291536000000"},
		"vehicleId":            {"1_3521"},
		"stopId":               {"1_75403"},
		"code":                 {"vehicle_never_came"},
		"userComment":          {"Test"},
		"userOnVehicle":        {"true"},
		"userVehicleNumber":    {"1234"},
		"userLat":              {"47.6097"},
		"userLon":              {"-122.3331"},
		"userLocationAccuracy": {"10"},
	}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	nullResp, nullModel := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("", url.Values{
		"code": {"vehicle_never_came"},
	}))

	assert.Equal(t, http.StatusBadRequest, nullResp.StatusCode, "Should return 400 when ID is missing")
	assert.Equal(t, http.StatusBadRequest, nullModel.Code)
	assert.Equal(t, "id cannot be empty", nullModel.Text)
}

func TestReportProblemWithTripMinimalParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_12345"))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
}

func TestReportProblemWithTripSanitization(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_12345", url.Values{
		"code":    {"vehicle_never_came"},
		"userLat": {"invalid"},
		"userLon": {"not_a_number"},
	}))

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should handle invalid userLat/userLon gracefully without 500 error")
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	longComment := strings.Repeat("a", 1000)
	respLong, modelLong := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_12345", url.Values{
		"code":        {"vehicle_never_came"},
		"userComment": {longComment},
	}))

	assert.Equal(t, http.StatusOK, respLong.StatusCode, "Should handle massive user comments gracefully")
	assert.Equal(t, http.StatusOK, modelLong.Code)
}
