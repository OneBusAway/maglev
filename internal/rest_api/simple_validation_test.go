package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleValidationErrors(t *testing.T) {
	api := createTestApi(t)

	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
	}{
		{
			name:           "Invalid agency ID with special characters",
			endpoint:       "/api/where/agency/bad<script>.json?key=TEST",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid location - latitude too high",
			endpoint:       "/api/where/stops-for-location.json?key=TEST&lat=91.0&lon=-77.0",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Valid agency ID should work",
			endpoint:       "/api/where/agency/raba.json?key=TEST",
			expectedStatus: http.StatusOK, // Might be 404 if agency doesn't exist, but not 400
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, _ := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)
			assert.Equal(t, tt.expectedStatus, response.StatusCode, "Expected status code mismatch")
			
			if tt.expectedStatus == http.StatusBadRequest {
				// Read the response body to check error format
				bodyBytes, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				
				var errorResponse map[string]interface{}
				err = json.Unmarshal(bodyBytes, &errorResponse)
				require.NoError(t, err)
				
				// Should have some kind of error structure
				assert.Contains(t, string(bodyBytes), "error", "Error response should contain error information")
			}
		})
	}
}

func TestValidationBoundaryConditions(t *testing.T) {
	api := createTestApi(t)

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