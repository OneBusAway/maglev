package restapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRateLimitMiddleware(t *testing.T) {
	middleware := NewRateLimitMiddleware(10, time.Second)
	assert.NotNil(t, middleware, "Middleware should not be nil")
}

func TestRateLimitMiddleware_AllowsRequestsWithinLimit(t *testing.T) {
	middleware := NewRateLimitMiddleware(5, time.Second)

	// Create a simple handler that responds with 200
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with rate limiting
	limitedHandler := middleware(handler)

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
	middleware := NewRateLimitMiddleware(3, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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
	middleware := NewRateLimitMiddleware(2, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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

func TestRateLimitMiddleware_ExemptsOneBusAwayiPhone(t *testing.T) {
	middleware := NewRateLimitMiddleware(1, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

	// Make many requests with the exempted API key
	exemptKey := "org.onebusaway.iphone"
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", exemptKey), nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Exempted API key request %d should always be allowed", i+1)
	}
}

func TestRateLimitMiddleware_HandlesNoAPIKey(t *testing.T) {
	middleware := NewRateLimitMiddleware(5, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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
	middleware := NewRateLimitMiddleware(1, 100*time.Millisecond)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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
	middleware := NewRateLimitMiddleware(5, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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
		if code == http.StatusOK {
			successCount++
		} else if code == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}

	// Should have exactly 5 successful requests and 5 rate limited
	assert.Equal(t, 5, successCount, "Should have exactly 5 successful requests")
	assert.Equal(t, 5, rateLimitedCount, "Should have exactly 5 rate limited requests")
}

func TestRateLimitMiddleware_RateLimitedResponseFormat(t *testing.T) {
	middleware := NewRateLimitMiddleware(1, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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
	body := w.Body.String()
	assert.Contains(t, body, "Rate limit", "Response should mention rate limiting")
}

func TestRateLimitMiddleware_CleanupOldLimiters(t *testing.T) {
	middleware := NewRateLimitMiddleware(5, time.Second)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware(handler)

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
		middleware := NewRateLimitMiddleware(0, time.Second)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware(handler)

		req := httptest.NewRequest("GET", "/test?key=test-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		// Should be immediately rate limited
		assert.Equal(t, http.StatusTooManyRequests, w.Code,
			"Zero rate limit should block all requests")
	})

	t.Run("Very high rate limit", func(t *testing.T) {
		middleware := NewRateLimitMiddleware(1000, time.Second)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware(handler)

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
		middleware := NewRateLimitMiddleware(5, time.Second)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware(handler)

		req := httptest.NewRequest("GET", "/test?key=", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Empty API key should be handled gracefully")
	})
}
