package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestGtfsExpiryMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		manager        *gtfs.Manager
		setupManager   func(*gtfs.Manager)
		expectedHeader string
	}{
		{
			name:           "Valid feed data - no header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        &gtfs.Manager{},
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAt(time.Now().Add(24 * time.Hour)) },
			expectedHeader: "",
		},
		{
			name:           "Expired feed data - adds header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        &gtfs.Manager{},
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAt(time.Now().Add(-24 * time.Hour)) },
			expectedHeader: "true",
		},
		{
			name:           "Nil manager - does not panic and no header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        nil,
			setupManager:   nil,
			expectedHeader: "",
		},
		{
			name:           "Zero expiry time (no calendar data) - no header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        &gtfs.Manager{},
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAt(time.Time{}) },
			expectedHeader: "",
		},
		{
			name:           "Expired feed data on non-API path - no header",
			path:           "/healthz",
			manager:        &gtfs.Manager{},
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAt(time.Now().Add(-24 * time.Hour)) },
			expectedHeader: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.manager != nil && tc.setupManager != nil {
				tc.setupManager(tc.manager)
			}

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := GtfsExpiryMiddleware(tc.manager)
			handler := middleware(nextHandler)
			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.expectedHeader, w.Header().Get("X-Data-Expired"))
		})
	}
}
