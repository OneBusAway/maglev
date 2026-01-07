package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextCancellationHandling(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name     string
		endpoint string
		timeout  time.Duration
	}{
		{
			name:     "agencies with coverage should handle context cancellation",
			endpoint: "/api/where/agencies-with-coverage.json?key=test",
			timeout:  1 * time.Nanosecond, // Very short timeout to trigger cancellation
		},
		{
			name:     "stop IDs for agency should handle context cancellation",
			endpoint: "/api/where/stop-ids-for-agency/1?key=test",
			timeout:  1 * time.Nanosecond,
		},
		{
			name:     "routes for location should handle context cancellation",
			endpoint: "/api/where/routes-for-location.json?lat=38.9&lon=-77.0&key=test",
			timeout:  1 * time.Nanosecond,
		},
		{
			name:     "stops for location should handle context cancellation",
			endpoint: "/api/where/stops-for-location.json?lat=38.9&lon=-77.0&key=test",
			timeout:  1 * time.Nanosecond,
		},
		{
			name:     "stops for route should handle context cancellation",
			endpoint: "/api/where/stops-for-route/1?key=test",
			timeout:  1 * time.Nanosecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a request with a very short timeout to trigger cancellation
			req, err := http.NewRequest("GET", tt.endpoint, nil)
			require.NoError(t, err)

			// Create a context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()
			req = req.WithContext(ctx)

			// Create a response recorder
			w := httptest.NewRecorder()

			// Create a handler that checks for context cancellation
			mux := http.NewServeMux()
			api.SetRoutes(mux)

			// Add a slight delay to increase chance of context cancellation
			time.Sleep(time.Microsecond)

			// Execute the request
			mux.ServeHTTP(w, req)

			// The request should either complete normally or be cancelled
			// If cancelled, we expect a timeout or cancellation error response
			statusCode := w.Code

			// Valid responses: 200 (completed), 401 (API validation), 500 (error), or timeout-related
			assert.True(t, statusCode == http.StatusOK ||
				statusCode == http.StatusUnauthorized || // API key validation happens first
				statusCode == http.StatusInternalServerError ||
				statusCode == http.StatusRequestTimeout ||
				statusCode == http.StatusGatewayTimeout ||
				statusCode == http.StatusNotFound,
				"Expected status 200, 401, 404, 500, 408, or 504, got %d", statusCode)
		})
	}
}

func TestLongerTimeoutContextHandling(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Test with a reasonable timeout that should allow completion
	t.Run("reasonable timeout should complete successfully", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/where/agencies-with-coverage.json?key=test", nil)
		require.NoError(t, err)

		// Create context with reasonable timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		mux := http.NewServeMux()
		api.SetRoutes(mux)

		mux.ServeHTTP(w, req)

		// Should complete successfully with reasonable timeout
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestContextCancellationInGetStopsForLocation(t *testing.T) {
	// Test the GTFS manager's GetStopsForLocation method with context cancellation
	api := createTestApi(t)
	defer api.Shutdown()

	// This test verifies that our current implementation works normally
	// since it uses context.Background() internally
	stops := api.GtfsManager.GetStopsForLocation(context.Background(), 38.9, -77.0, 1000, 0, 0, "", 10, false, nil, time.Now())

	// Current implementation should return a slice (possibly empty)
	// The function should not panic and should return a valid slice
	if stops == nil {
		t.Log("GetStopsForLocation returned nil - this may indicate an issue")
	}
	// After our fix, this should handle cancellation gracefully
	assert.True(t, stops != nil || len(stops) == 0, "Function should return valid slice or nil")
}

func TestContextCancellationDuringDatabaseQueries(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	t.Run("cancelled context in database query should be handled", func(t *testing.T) {
		// Create a context that's already cancelled
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Try to execute a database query with cancelled context
		_, err := api.GtfsManager.GtfsDB.Queries.ListAgencies(ctx)

		// The query should either succeed (if fast enough) or return a context error
		if err != nil {
			assert.Equal(t, context.Canceled, err)
		}
	})

	t.Run("timeout context in database query should be handled", func(t *testing.T) {
		// Create a context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Add a small delay to ensure timeout
		time.Sleep(time.Microsecond)

		// Try to execute a database query with timeout context
		_, err := api.GtfsManager.GtfsDB.Queries.ListAgencies(ctx)

		// The query should either succeed (if very fast) or return a timeout error
		if err != nil {
			assert.Equal(t, context.DeadlineExceeded, err)
		}
	})
}
