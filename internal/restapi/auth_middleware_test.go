package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

func TestValidateProtectedAPIKey(t *testing.T) {
	// mock application with protected API key
	mockApp := &app.Application{
		Clock: &clock.RealClock{},
		Config: appconf.Config{
			ProtectedApiKeys: []string{"secret-admin-key", "another-secret-key"},
		},
	}
	api := NewRestAPI(mockApp)

	// dummy handler act as the "next" handler in the chain
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Success"))
	})

	tests := []struct {
		name           string
		key            string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "Valid Protected Key 1",
			key:            "secret-admin-key",
			expectedStatus: http.StatusOK,
			expectedBody:   "Success",
		},
		{
			name:           "Valid Protected Key 2",
			key:            "another-secret-key",
			expectedStatus: http.StatusOK,
			expectedBody:   "Success",
		},
		{
			name:           "Missing Key",
			key:            "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid Key",
			key:            "invalid-key",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Standard API Key (unauthorized for protected)",
			key:            "test",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/api/protected?key="+tc.key, nil)
			assert.NoError(t, err)

			rr := httptest.NewRecorder()

			handler := api.validateProtectedAPIKey(nextHandler)

			handler.ServeHTTP(rr, req)

			assert.Equal(t, tc.expectedStatus, rr.Code)
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedBody, rr.Body.String())
			} else {
				var resp models.ResponseModel
				err = json.Unmarshal(rr.Body.Bytes(), &resp)
				assert.NoError(t, err)

				assert.Equal(t, tc.expectedStatus, resp.Code)
				assert.Equal(t, "permission denied", resp.Text)
				assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			}
		})
	}
}
