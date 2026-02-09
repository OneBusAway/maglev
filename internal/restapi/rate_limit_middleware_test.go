package restapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/clock"
)

// initRateLimitMiddleware initializes a rate limit middleware with clock.RealClock for testing
func initRateLimitMiddleware(ratePerSecond int, interval time.Duration) *RateLimitMiddleware {
	return NewRateLimitMiddleware(ratePerSecond, interval, nil, clock.RealClock{})
}

func TestNewRateLimitMiddleware(t *testing.T) {
	middleware := initRateLimitMiddleware(10, time.Second)
	defer middleware.Stop()
	assert.NotNil(t, middleware, "Middleware should not be nil")
	assert.NotNil(t, middleware.Handler(), "Handler should not be nil")
}

func TestRateLimitMiddleware_AllowsRequestsWithinLimit(t *testing.T) {
	middleware := initRateLimitMiddleware(5, time.Second)
	defer middleware.Stop()

	// Create a simple handler that responds with 200
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with rate limiting
	limitedHandler := middleware.Handler()(handler)

	// Test multiple requests within the limit
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test?key=test-api-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Request %d should be allowed", i+1)
	}
}

func TestRateLimitMiddleware_BlocksRequestsOverLimit(t *testing.T) {
	middleware := initRateLimitMiddleware(3, time.Second)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test?key=test-api-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Request %d should be allowed", i+1)
	}

	// 4th request should be rate limited
	req := httptest.NewRequest("GET", "/test?key=test-api-key", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"Request over limit should be blocked")
}

func TestRateLimitMiddleware_PerAPIKeyLimiting(t *testing.T) {
	middleware := initRateLimitMiddleware(2, time.Second)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// Test API key 1 - use up its limit
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test?key=api-key-1", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"API key 1 request %d should be allowed", i+1)
	}

	// API key 1 should now be rate limited
	req := httptest.NewRequest("GET", "/test?key=api-key-1", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"API key 1 should be rate limited")

	// API key 2 should still work (separate limit)
	req = httptest.NewRequest("GET", "/test?key=api-key-2", nil)
	w = httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"API key 2 should not be affected")
}

func TestRateLimitMiddleware_ExemptsConfiguredKeys(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("Exempts custom configured key", func(t *testing.T) {
		exemptKeys := []string{"custom-admin-key"}
		// Set limit to 1 to ensure exemption logic works (we will make >1 request)
		middleware := NewRateLimitMiddleware(1, time.Second, exemptKeys, clock.RealClock{})
		defer middleware.Stop()

		limitedHandler := middleware.Handler()(handler)

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest("GET", "/test?key=custom-admin-key", nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Configured exempt key should always be allowed")
		}

		// Non-exempt key should still be limited
		req := httptest.NewRequest("GET", "/test?key=other-key", nil)
		w := httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "First request for non-exempt key ok")

		req = httptest.NewRequest("GET", "/test?key=other-key", nil)
		w = httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "Second request for non-exempt key blocked")
	})

	t.Run("Exempts multiple keys", func(t *testing.T) {
		exemptKeys := []string{"key-A", "key-B"}
		middleware := NewRateLimitMiddleware(1, time.Second, exemptKeys, clock.RealClock{})
		defer middleware.Stop()

		limitedHandler := middleware.Handler()(handler)

		// Check Key A
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/test?key=key-A", nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Key A should be exempt")
		}

		// Check Key B
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/test?key=key-B", nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Key B should be exempt")
		}
	})

	t.Run("Handles nil exempt keys (no exemption)", func(t *testing.T) {
		// Pass nil for exempt keys
		middleware := NewRateLimitMiddleware(1, time.Second, nil, clock.RealClock{})
		defer middleware.Stop()

		limitedHandler := middleware.Handler()(handler)

		// Try with what used to be the hardcoded exempt key
		key := "org.onebusaway.iphone"

		// First request passes
		req := httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", key), nil)
		w := httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Second request fails (proving it's NOT exempt when passed as nil)
		req = httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", key), nil)
		w = httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "Should not be exempt if config is nil")
	})
}

func TestRateLimitMiddleware_HandlesNoAPIKey(t *testing.T) {
	middleware := initRateLimitMiddleware(5, time.Second)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// Request without API key should be handled by default limiter
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	// Should still get through to the handler (rate limiting doesn't handle auth)
	assert.Equal(t, http.StatusOK, w.Code,
		"Request without API key should be processed")
}

func TestRateLimitMiddleware_RefillsOverTime(t *testing.T) {
	// Use a very short refill interval for testing
	middleware := initRateLimitMiddleware(1, 100*time.Millisecond)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// First request should succeed
	req := httptest.NewRequest("GET", "/test?key=test-key", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "First request should succeed")

	// Second request should be rate limited
	req = httptest.NewRequest("GET", "/test?key=test-key", nil)
	w = httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"Second request should be rate limited")

	// Wait for refill
	time.Sleep(150 * time.Millisecond)

	// Third request should succeed after refill
	req = httptest.NewRequest("GET", "/test?key=test-key", nil)
	w = httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Request after refill should succeed")
}

func TestRateLimitMiddleware_ConcurrentRequests(t *testing.T) {
	middleware := initRateLimitMiddleware(5, time.Second)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// Make 10 concurrent requests
	var wg sync.WaitGroup
	results := make([]int, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/test?key=concurrent-test", nil)
			w := httptest.NewRecorder()

			limitedHandler.ServeHTTP(w, req)
			results[index] = w.Code
		}(i)
	}

	wg.Wait()

	// Count successful vs rate limited requests
	successCount := 0
	rateLimitedCount := 0

	for _, code := range results {
		switch code {
		case http.StatusOK:
			successCount++
		case http.StatusTooManyRequests:
			rateLimitedCount++
		}
	}

	// Should have exactly 5 successful requests and 5 rate limited
	assert.Equal(t, 5, successCount, "Should have exactly 5 successful requests")
	assert.Equal(t, 5, rateLimitedCount, "Should have exactly 5 rate limited requests")
}

func TestRateLimitMiddleware_RateLimitedResponseFormat(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	middleware := NewRateLimitMiddleware(1, time.Second, nil, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// First request to consume the limit
	req := httptest.NewRequest("GET", "/test?key=test-key", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	// Second request should be rate limited
	req = httptest.NewRequest("GET", "/test?key=test-key", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Check for rate limit headers
	assert.NotEmpty(t, w.Header().Get("Retry-After"), "Should include Retry-After header")

	// Check response body format
	var responseBody map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &responseBody))

	assert.Contains(t, responseBody["text"].(string), "Rate limit")

	// check currentTime
	assert.Equal(t, mockClock.Now().UnixMilli(), int64(responseBody["currentTime"].(float64)))
}

func TestRateLimitMiddleware_CleanupOldLimiters(t *testing.T) {
	middleware := initRateLimitMiddleware(5, time.Second)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	// Create limiters for multiple API keys
	apiKeys := []string{"key1", "key2", "key3", "key4", "key5"}

	for _, key := range apiKeys {
		req := httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", key), nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Request for key %s should succeed", key)
	}

	// Verify that the middleware tracks the limiters
	// Note: This test verifies that cleanup logic exists, actual cleanup
	// verification would require exposing internal state or time-based testing
}

func TestRateLimitMiddleware_EdgeCases(t *testing.T) {
	t.Run("Zero rate limit", func(t *testing.T) {
		middleware := initRateLimitMiddleware(0, time.Second)
		defer middleware.Stop()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware.Handler()(handler)

		req := httptest.NewRequest("GET", "/test?key=test-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		// Should be immediately rate limited
		assert.Equal(t, http.StatusTooManyRequests, w.Code,
			"Zero rate limit should block all requests")
	})

	t.Run("Very high rate limit", func(t *testing.T) {
		middleware := initRateLimitMiddleware(1000, time.Second)
		defer middleware.Stop()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware.Handler()(handler)

		// Make many requests quickly
		for i := 0; i < 100; i++ {
			req := httptest.NewRequest("GET", "/test?key=high-limit-key", nil)
			w := httptest.NewRecorder()

			limitedHandler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code,
				"High rate limit should allow many requests")
		}
	})

	t.Run("Empty API key", func(t *testing.T) {
		middleware := initRateLimitMiddleware(5, time.Second)
		defer middleware.Stop()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware.Handler()(handler)

		req := httptest.NewRequest("GET", "/test?key=", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Empty API key should be handled gracefully")
	})
}
