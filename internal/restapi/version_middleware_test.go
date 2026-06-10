package restapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

func TestVersionValidationMiddleware(t *testing.T) {
	tests := []struct {
		name               string
		path               string
		expectedStatus     int
		expectNextCalled   bool
		expectedErrMessage string
	}{
		{
			name:             "Valid version passes through",
			path:             "/api/where/route-ids-for-agency/test.json?version=2",
			expectedStatus:   http.StatusOK,
			expectNextCalled: true,
		},
		{
			name:               "Invalid version returns 400",
			path:               "/api/where/route-ids-for-agency/test.json?version=99",
			expectedStatus:     http.StatusBadRequest,
			expectNextCalled:   false,
			expectedErrMessage: "unknown version: 99",
		},
		{
			name:             "Absent version passes through (defaults to v2)",
			path:             "/api/where/route-ids-for-agency/test.json",
			expectedStatus:   http.StatusOK,
			expectNextCalled: true,
		},
		{
			name:             "Empty version passes through (defaults to v2)",
			path:             "/api/where/route-ids-for-agency/test.json?version=",
			expectedStatus:   http.StatusOK,
			expectNextCalled: true,
		},
		{
			name:             "Non-API path with invalid version is not checked",
			path:             "/healthz?version=99",
			expectedStatus:   http.StatusOK,
			expectNextCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &RestAPI{
				Application: &app.Application{
					Logger: slog.Default(),
					Clock:  clock.RealClock{},
				},
			}

			nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			handler := api.VersionValidationMiddleware(nextHandler)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
			assert.Equal(t, tc.expectNextCalled, nextCalled)

			if tc.expectedErrMessage != "" {
				var response models.ResponseModel
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.Equal(t, http.StatusBadRequest, response.Code)
				assert.Equal(t, tc.expectedErrMessage, response.Text)
				assert.Equal(t, models.APIVersion, response.Version)
			}
		})
	}
}
