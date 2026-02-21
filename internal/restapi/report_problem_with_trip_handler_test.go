package restapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportProblemWithTripRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	payload := ReportProblemTrip{CompositeID: "1_12345.json"}
	body, _ := json.Marshal(payload)

	resp, model := serveApiAndRetrieveEndpointWithBody(
		t,
		api,
		http.MethodPost,
		"/api/where/report-problem-with-trip?key=invalid",
		body,
	)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithTripEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	lat, lon, acc := 47.6097, -122.3331, 10.0

	payload := ReportProblemTrip{
		CompositeID:          "1_12345.json",
		ServiceDate:          "1291536000000",
		VehicleID:            "1_3521",
		StopID:               "1_75403",
		Code:                 "vehicle_never_came",
		UserComment:          "Test",
		UserOnVehicle:        "true",
		UserVehicleNumber:    "1234",
		UserLat:              &lat,
		UserLon:              &lon,
		UserLocationAccuracy: &acc,
	}

	body, _ := json.Marshal(payload)
	resp, model := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-trip?key=TEST", body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	payload.CompositeID = ""
	bodyEmptyID, _ := json.Marshal(payload)
	resp400, model400 := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-trip?key=TEST", bodyEmptyID)

	assert.Equal(t, http.StatusBadRequest, resp400.StatusCode)
	assert.Equal(t, "id cannot be empty", model400.Text)
}

func TestReportProblemWithTrip_MinimalParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	payload := ReportProblemTrip{
		CompositeID: "1_12345.json",
	}

	body, _ := json.Marshal(payload)
	resp, model := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-trip?key=TEST", body)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 200, model.Code)
}

func TestReportProblemWithTripSanitization(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	invalidJSON := []byte(`{"composite_id": "1_12345.json", "user_lat": "invalid"}`)
	resp, _ := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-trip?key=TEST", invalidJSON)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Invalid JSON types should return 400")

	longComment := strings.Repeat("a", 1000)
	payload := ReportProblemTrip{
		CompositeID: "1_12345.json",
		UserComment: longComment,
	}

	body, _ := json.Marshal(payload)
	respLong, modelLong := serveApiAndRetrieveEndpointWithBody(t, api, http.MethodPost, "/api/where/report-problem-with-trip?key=TEST", body)

	assert.Equal(t, http.StatusOK, respLong.StatusCode)
	assert.Equal(t, 200, modelLong.Code)
}
