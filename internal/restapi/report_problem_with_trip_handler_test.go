package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
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

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)

	longComment := strings.Repeat("a", 1000)
	respLong, modelLong := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_12345", url.Values{
		"code":        {"vehicle_never_came"},
		"userComment": {longComment},
	}))

	assert.Equal(t, http.StatusOK, respLong.StatusCode, "Should handle massive user comments gracefully")
	assert.Equal(t, http.StatusOK, modelLong.Code)
}

// TestReportProblemWithTripCoordinateRangeValidation verifies that out-of-range userLat
// and userLon parameters return HTTP 400 with validation errors instead of database 500s.
func TestReportProblemWithTripCoordinateRangeValidation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)

	tests := []struct {
		name           string
		params         url.Values
		expectedErrors map[string]string
	}{
		{name: "userLat too high", params: url.Values{"userLat": {"100"}, "userLon": {"-122.3331"}}, expectedErrors: map[string]string{"userLat": "latitude must be between -90 and 90"}},
		{name: "userLat too low", params: url.Values{"userLat": {"-95"}, "userLon": {"-122.3331"}}, expectedErrors: map[string]string{"userLat": "latitude must be between -90 and 90"}},
		{name: "userLon too high", params: url.Values{"userLat": {"47.6097"}, "userLon": {"200"}}, expectedErrors: map[string]string{"userLon": "longitude must be between -180 and 180"}},
		{name: "userLon too low", params: url.Values{"userLat": {"47.6097"}, "userLon": {"-200"}}, expectedErrors: map[string]string{"userLon": "longitude must be between -180 and 180"}},
		{name: "both coordinates out of range", params: url.Values{"userLat": {"95"}, "userLon": {"190"}}, expectedErrors: map[string]string{
			"userLat": "latitude must be between -90 and 90",
			"userLon": "longitude must be between -180 and 180",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[strictValidationResponse](t, api, reportProblemWithTripURL("1_12345", tt.params))
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
			for field, msg := range tt.expectedErrors {
				assert.Contains(t, model.Data.FieldErrors[field], msg)
			}
		})
	}

	for _, bt := range []struct {
		name, lat, lon string
	}{
		{name: "max valid coordinates", lat: "90", lon: "180"},
		{name: "min valid coordinates", lat: "-90", lon: "-180"},
	} {
		t.Run(bt.name, func(t *testing.T) {
			resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_boundary_trip", url.Values{
				"userLat": {bt.lat},
				"userLon": {bt.lon},
			}))
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
		})
	}
}

func TestReportProblemWithTripDatabaseFailure(t *testing.T) {
	api := apiWithClosedDB(t)

	resp, model := callAPIHandler[models.ResponseModel](t, api, reportProblemWithTripURL("1_12345"))
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, http.StatusInternalServerError, model.Code)
	assert.Equal(t, "internal server error", model.Text)
	assert.Equal(t, models.APIVersion, model.Version)
}

func TestReportProblemWithTripNullStorage(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[EmptyResponse](t, api, reportProblemWithTripURL("1_12345", url.Values{
		"vehicleId": {"1_3521"},
		"stopId":    {"1_75403"},
	}))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)

	reports, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByTrip(t.Context(), "12345")
	require.NoError(t, err)
	require.NotEmpty(t, reports)

	latest := reports[0]
	assert.True(t, latest.VehicleID.Valid)
	assert.Equal(t, "1_3521", latest.VehicleID.String)
	assert.True(t, latest.StopID.Valid)
	assert.Equal(t, "1_75403", latest.StopID.String)
	assert.False(t, latest.Code.Valid, "omitted code should be stored as SQL NULL")
	assert.False(t, latest.UserComment.Valid, "omitted userComment should be stored as SQL NULL")
	assert.False(t, latest.UserVehicleNumber.Valid, "omitted userVehicleNumber should be stored as SQL NULL")
}
