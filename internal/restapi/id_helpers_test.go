package restapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
)

func TestExtractAndValidateID(t *testing.T) {
	api := &RestAPI{
		Application: &app.Application{
			Clock: &clock.RealClock{},
		},
	}

	tests := []struct {
		name           string
		url            string
		idParam        string
		expectedID     string
		expectedOk     bool
		expectedStatus int
	}{
		{
			name:           "Valid simple ID",
			url:            "/api/where/route/1_100229.json",
			idParam:        "1_100229.json",
			expectedID:     "1_100229",
			expectedOk:     true,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Empty ID",
			url:            "/api/where/route/.json",
			idParam:        ".json",
			expectedID:     "",
			expectedOk:     false,
			expectedStatus: http.StatusBadRequest,
		},

	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), "GET", tt.url, nil)
			require.NoError(t, err)
			req.SetPathValue("id", tt.idParam)

			rr := httptest.NewRecorder()
			id, ok := api.extractAndValidateID(rr, req)

			assert.Equal(t, tt.expectedOk, ok)
			if ok {
				assert.Equal(t, tt.expectedID, id)
				assert.Empty(t, rr.Body.String())
			} else {
				assert.Equal(t, "", id)
				assert.Equal(t, tt.expectedStatus, rr.Code)
				var errorResp struct {
					Code int `json:"code"`
				}
				err = json.Unmarshal(rr.Body.Bytes(), &errorResp)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedStatus, errorResp.Code)
				assert.Contains(t, rr.Body.String(), "id") // Should contain the validation error field
			}
		})
	}
}

func TestExtractAndValidateAgencyCodeID(t *testing.T) {
	api := &RestAPI{
		Application: &app.Application{
			Clock: &clock.RealClock{},
		},
	}

	tests := []struct {
		name             string
		url              string
		idParam          string
		expectedAgencyID string
		expectedCodeID   string
		expectedOk       bool
		expectedStatus   int
	}{
		{
			name:             "Valid combined ID",
			url:              "/api/where/route/1_100229.json",
			idParam:          "1_100229.json",
			expectedAgencyID: "1",
			expectedCodeID:   "100229",
			expectedOk:       true,
			expectedStatus:   http.StatusOK,
		},
		{
			name:             "Invalid combined ID (no underscore)",
			url:              "/api/where/route/100229.json",
			idParam:          "100229.json",
			expectedAgencyID: "",
			expectedCodeID:   "",
			expectedOk:       false,
			expectedStatus:   http.StatusBadRequest,
		},
		{
			name:             "Empty ID",
			url:              "/api/where/route/.json",
			idParam:          ".json",
			expectedAgencyID: "",
			expectedCodeID:   "",
			expectedOk:       false,
			expectedStatus:   http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), "GET", tt.url, nil)
			require.NoError(t, err)
			req.SetPathValue("id", tt.idParam)

			rr := httptest.NewRecorder()
			agencyID, codeID, ok := api.extractAndValidateAgencyCodeID(rr, req)

			assert.Equal(t, tt.expectedOk, ok)
			if ok {
				assert.Equal(t, tt.expectedAgencyID, agencyID)
				assert.Equal(t, tt.expectedCodeID, codeID)
				assert.Empty(t, rr.Body.String())
			} else {
				assert.Equal(t, "", agencyID)
				assert.Equal(t, "", codeID)
				assert.Equal(t, tt.expectedStatus, rr.Code)
				var errorResp struct {
					Code int `json:"code"`
				}
				err = json.Unmarshal(rr.Body.Bytes(), &errorResp)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedStatus, errorResp.Code)
				assert.True(t, strings.Contains(rr.Body.String(), "id") || strings.Contains(rr.Body.String(), "invalid format"))
			}
		})
	}
}
