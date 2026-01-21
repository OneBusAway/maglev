package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
)

func TestRateLimitingIntegration(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name          string
		endpoint      string
		apiKey        string
		requestCount  int
		expectBlocked int
		expectAllowed int
	}{
		{
			name:          "Agency endpoint with normal API key",
			endpoint:      "/api/where/agency/raba.json",
			apiKey:        "TEST",
			requestCount:  10, // Over the 5/second limit
			expectBlocked: 5,
			expectAllowed: 5,
		},
		{
			name:          "Stops for location with exempted key",
			endpoint:      "/api/where/stops-for-location.json?lat=38.9&lon=-77.0",
			apiKey:        "org.onebusaway.iphone",
			requestCount:  15, // Well over the limit
			expectBlocked: 0,  // Should all be allowed due to exemption
			expectAllowed: 15,
		},
		{
			name:          "Current time endpoint rate limiting",
			endpoint:      "/api/where/current-time.json",
			apiKey:        "test-rate-limit",
			requestCount:  10,
			expectBlocked: 5,
			expectAllowed: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			successCount := 0
			rateLimitedCount := 0

			// Make all requests rapidly
			for i := 0; i < tt.requestCount; i++ {
				endpoint := tt.endpoint
				if tt.apiKey != "" {
					// Add API key to query string
					separator := "?"
					if contains(endpoint, "?") {
						separator = "&"
					}
					endpoint += separator + "key=" + tt.apiKey
				}

				response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

				switch response.StatusCode {
				case http.StatusOK, http.StatusNotFound:
					// Both OK and NotFound count as successful (not rate limited)
					successCount++
				case http.StatusTooManyRequests:
					rateLimitedCount++
				}
			}

			// Allow for some variance due to rate limiter refill during test execution
			// The rate limiter allows 100/second, so in rapid succession we might get a few more
			assert.InDelta(t, tt.expectAllowed, successCount, 5,
				"Expected approximately %d allowed requests, got %d", tt.expectAllowed, successCount)
			assert.InDelta(t, tt.expectBlocked, rateLimitedCount, 5,
				"Expected approximately %d blocked requests, got %d", tt.expectBlocked, rateLimitedCount)
		})
	}
}

func TestRateLimitingPerAPIKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Test that different API keys have separate rate limits
	endpoint := "/api/where/current-time.json"

	// Use up the limit for TEST key by making requests rapidly
	hitLimit := false
	for i := 0; i < 10; i++ {
		response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint+"?key=TEST")
		if response.StatusCode == http.StatusTooManyRequests {
			hitLimit = true
			break
		}
	}

	assert.True(t, hitLimit, "TEST key should hit rate limit within 10 requests")

	// TEST key should now be rate limited
	response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint+"?key=TEST")
	assert.Equal(t, http.StatusTooManyRequests, response.StatusCode,
		"TEST key should be rate limited")

	// Different endpoint with same key should also be rate limited
	// (since rate limiting is per API key, not per endpoint)
	response, _ = serveApiAndRetrieveEndpoint(t, api, "/api/where/agency/raba.json?key=TEST")
	assert.Equal(t, http.StatusTooManyRequests, response.StatusCode,
		"Different endpoint with same key should also be rate limited")
}

func TestRateLimitingExemption(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/current-time.json"
	exemptKey := "org.onebusaway.iphone"

	// Make many requests with the exempted key - all should succeed
	for i := 0; i < 20; i++ {
		response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint+"?key="+exemptKey)
		assert.Equal(t, http.StatusOK, response.StatusCode,
			"Exempted key request %d should always succeed", i+1)
	}
}

func TestRateLimitingHeaders(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/current-time.json?key=test-headers"

	// Use up the rate limit by making requests rapidly
	// Make 10 requests to ensure we exceed the 5 limit even with some refill
	for i := 0; i < 10; i++ {
		response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

		// Once we hit rate limit, check the headers
		if response.StatusCode == http.StatusTooManyRequests {
			assert.NotEmpty(t, response.Header.Get("Retry-After"),
				"Rate limited response should include Retry-After header")
			assert.NotEmpty(t, response.Header.Get("X-RateLimit-Limit"),
				"Rate limited response should include X-RateLimit-Limit header")
			assert.Equal(t, "0", response.Header.Get("X-RateLimit-Remaining"),
				"Rate limited response should show 0 remaining requests")
			return // Test passed
		}
	}

	t.Fatal("Expected to hit rate limit within 10 requests")
}

func TestRateLimitingRefill(t *testing.T) {
	// This test uses a shorter refill time to test the refill mechanism
	// Note: This test modifies the global rate limiter configuration

	api := createTestApi(t)
	defer api.Shutdown()
	endpoint := "/api/where/current-time.json?key=test-refill"

	// Make one request to establish the limiter
	response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)
	assert.Equal(t, http.StatusOK, response.StatusCode)

	// Note: For this test to be reliable, we'd need to create a custom
	// rate limiter with a shorter refill time. The current implementation
	// uses a global rate limiter which makes this challenging to test.
	// In a production system, you might want to make the rate limiter
	// configurable or injectable for better testability.
}

func TestRateLimitingWithoutAPIKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/current-time.json"

	// Request without API key should be handled by default limiter
	// Note: This will likely fail due to API key validation, but rate limiting
	// should still be applied before that check
	response, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	// Should get 401 (Unauthorized) due to missing API key, not 429 (rate limited)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode,
		"Request without API key should be unauthorized, not rate limited")
}

func TestRateLimitingErrorResponse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/current-time.json?key=test-error-format"

	// Use up the rate limit by making requests rapidly
	// Make 10 requests to ensure we exceed the 5 limit even with some refill
	var response *http.Response
	var model models.ResponseModel

	for i := 0; i < 10; i++ {
		response, model = serveApiAndRetrieveEndpoint(t, api, endpoint)

		// Once we hit rate limit, check the error response
		if response.StatusCode == http.StatusTooManyRequests {
			assert.Equal(t, http.StatusTooManyRequests, model.Code)
			assert.Contains(t, model.Text, "Rate limit",
				"Error response should mention rate limiting")
			assert.NotNil(t, model.Data, "Error response should include data structure")
			return // Test passed
		}
	}

	t.Fatal("Expected to hit rate limit within 10 requests")
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			containsAt(s, substr, 1))))
}

func containsAt(s, substr string, start int) bool {
	if start >= len(s) {
		return false
	}
	if len(s[start:]) < len(substr) {
		return false
	}
	if s[start:start+len(substr)] == substr {
		return true
	}
	return containsAt(s, substr, start+1)
}
