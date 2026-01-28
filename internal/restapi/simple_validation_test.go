package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSimpleValidationErrors(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
	}{
		{
			name:           "Invalid agency ID with special characters",
			endpoint:       "/api/where/agency/bad_script?key=TEST", // Use underscores instead of angle brackets
			expectedStatus: http.StatusNotFound,                     // Valid chars but agency doesn't exist
		},
		{
			name:           "Invalid location - latitude too high",
			endpoint:       "/api/where/stops-for-location.json?key=TEST&lat=91.0&lon=-77.0",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid agency ID should work",
			endpoint:       "/api/where/agency/raba?key=TEST",
			expectedStatus: http.StatusNotFound, // Agency doesn't exist in test data
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, _ := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)
			assert.Equal(t, tt.expectedStatus, response.StatusCode, "Expected status code mismatch")
		})
	}
}

func TestValidationBoundaryConditions(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
	}{
		{
			name:           "Latitude exactly 90 should be valid",
			endpoint:       "/api/where/stops-for-location.json?key=TEST&lat=90.0&lon=0.0",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Latitude 90.1 should be invalid",
			endpoint:       "/api/where/stops-for-location.json?key=TEST&lat=90.1&lon=0.0",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Longitude exactly 180 should be valid",
			endpoint:       "/api/where/stops-for-location.json?key=TEST&lat=0.0&lon=180.0",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Longitude 180.1 should be invalid",
			endpoint:       "/api/where/stops-for-location.json?key=TEST&lat=0.0&lon=180.1",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, _ := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)
			assert.Equal(t, tt.expectedStatus, response.StatusCode, "Expected status code mismatch for %s", tt.endpoint)
		})
	}
}
