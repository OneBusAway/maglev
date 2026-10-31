package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/clock"
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
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAtForTest(context.Background(), time.Now().Add(24*time.Hour)) },
			expectedHeader: "",
		},
		{
			name:           "Expired feed data - adds header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAtForTest(context.Background(), time.Now().Add(-24*time.Hour)) },
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
			name:           "Nil GtfsDB - does not panic and no header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.GtfsDB = nil },
			expectedHeader: "",
		},
		{
			name:           "Nil Queries - does not panic and no header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.GtfsDB.Queries = nil },
			expectedHeader: "",
		},
		{
			name:           "Zero expiry time (no calendar data) - no header",
			path:           "/api/where/agencies-with-coverage.json",
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAtForTest(context.Background(), time.Time{}) },
			expectedHeader: "",
		},
		{
			name:           "Expired feed data on non-API path - no header",
			path:           "/healthz",
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAtForTest(context.Background(), time.Now().Add(-24*time.Hour)) },
			expectedHeader: "",
		},
		{
			name:           "Non-API path /api (without trailing slash) is intentionally excluded",
			path:           "/api",
			manager:        newTestManagerNoData(t),
			setupManager:   func(m *gtfs.Manager) { m.SetFeedExpiresAtForTest(context.Background(), time.Now().Add(-24*time.Hour)) },
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

			middleware := GtfsExpiryMiddleware(tc.manager, nil)
			handler := middleware(nextHandler)
			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.expectedHeader, w.Header().Get("X-Data-Expired"))
		})
	}
}

func TestGtfsExpiryMiddlewareUsesClock(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.MarkReady()

	expiresAt := time.Date(2025, time.June, 15, 0, 0, 0, 0, time.UTC)
	manager.SetFeedExpiresAtForTest(t.Context(), expiresAt)

	tests := []struct {
		name        string
		currentTime *clock.MockClock
		isExpired   string
	}{
		{
			name:        "Feed is valid well before expiration",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 5, 0, 0, 0, 0, time.UTC)),
			isExpired:   "",
		},
		{
			name:        "Feed is past expiration",
			currentTime: clock.NewMockClock(time.Date(2025, time.June, 20, 0, 0, 0, 0, time.UTC)),
			isExpired:   "true",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/", nil)
			w := httptest.NewRecorder()

			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := GtfsExpiryMiddleware(manager, test.currentTime)
			handler := middleware(nextHandler)
			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, test.isExpired, w.Header().Get("X-Data-Expired"))
		})
	}
}
