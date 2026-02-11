package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportProblemWithTripRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/report-problem-with-trip/12345.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestReportProblemWithTripEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tripId := "1_12345"

	url := fmt.Sprintf("/api/where/report-problem-with-trip/%s.json?key=TEST&serviceDate=1291536000000&vehicleId=1_3521&stopId=1_75403&code=vehicle_never_came&userComment=Test&userOnVehicle=true&userVehicleNumber=1234&userLat=47.6097&userLon=-122.3331&userLocationAccuracy=10", tripId)

	resp, model := serveApiAndRetrieveEndpoint(t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	assert.Empty(t, data, "Data should be an empty object")

	nullURL := "/api/where/report-problem-with-trip/.json?key=TEST&code=vehicle_never_came"
	nullResp, nullModel := serveApiAndRetrieveEndpoint(t, api, nullURL)

	assert.Equal(t, http.StatusBadRequest, nullResp.StatusCode, "Should return 400 when ID is missing")
	assert.Equal(t, 400, nullModel.Code)
	assert.Equal(t, "id cannot be empty", nullModel.Text)
}
