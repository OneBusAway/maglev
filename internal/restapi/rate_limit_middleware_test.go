package restapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRateLimitMiddleware(t *testing.T) {
	middleware := NewRateLimitMiddleware(10, time.Second, nil)
	assert.NotNil(t, middleware, "Middleware should not be nil")
	assert.NotNil(t, middleware.Handler(), "Handler should not be nil")
}

func TestRateLimitMiddleware_AllowsRequestsWithinLimit(t *testing.T) {
	middleware := NewRateLimitMiddleware(5, time.Second, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/test?key=test-api-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Request %d should be allowed", i+1)

		assert.Equal(t, "5", w.Header().Get("X-RateLimit-Limit"), "Should set X-RateLimit-Limit")
		remainingStr := w.Header().Get("X-RateLimit-Remaining")
		assert.NotEmpty(t, remainingStr, "Should set X-RateLimit-Remaining")
		_, err := strconv.Atoi(remainingStr)
		assert.NoError(t, err)
	}
}

func TestRateLimitMiddleware_BlocksRequestsOverLimit(t *testing.T) {
	middleware := NewRateLimitMiddleware(3, time.Second, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test?key=test-api-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code,
			"Request %d should be allowed", i+1)
	}

	req := httptest.NewRequest("GET", "/test?key=test-api-key", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"Request over limit should be blocked")
}

func TestRateLimitMiddleware_ExemptsConfiguredKeys(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("Exempts custom configured key", func(t *testing.T) {
		exemptKeys := []string{"custom-admin-key"}
		middleware := NewRateLimitMiddleware(1, time.Second, exemptKeys)

		limitedHandler := middleware.Handler()(handler)

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest("GET", "/test?key=custom-admin-key", nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Configured exempt key should always be allowed")
		}

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
		middleware := NewRateLimitMiddleware(1, time.Second, exemptKeys)

		limitedHandler := middleware.Handler()(handler)

		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/test?key=key-A", nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Key A should be exempt")
		}

		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/test?key=key-B", nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "Key B should be exempt")
		}
	})

	t.Run("Handles nil exempt keys (no exemption)", func(t *testing.T) {
		middleware := NewRateLimitMiddleware(1, time.Second, nil)

		limitedHandler := middleware.Handler()(handler)

		key := "org.onebusaway.iphone"

		req := httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", key), nil)
		w := httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		req = httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", key), nil)
		w = httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, "Should not be exempt if config is nil")
	})
}

func TestRateLimitMiddleware_HandlesNoAPIKey(t *testing.T) {
	middleware := NewRateLimitMiddleware(5, time.Second, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Request without API key should be processed")
}

func TestRateLimitMiddleware_RefillsOverTime(t *testing.T) {
	middleware := NewRateLimitMiddleware(1, 100*time.Millisecond, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	req := httptest.NewRequest("GET", "/test?key=test-key", nil)
	w := httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "First request should succeed")

	req = httptest.NewRequest("GET", "/test?key=test-key", nil)
	w = httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code,
		"Second request should be rate limited")

	time.Sleep(150 * time.Millisecond)

	req = httptest.NewRequest("GET", "/test?key=test-key", nil)
	w = httptest.NewRecorder()

	limitedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Request after refill should succeed")
}

func TestRateLimitMiddleware_ConcurrentRequests(t *testing.T) {
	middleware := NewRateLimitMiddleware(5, time.Second, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

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

	assert.Equal(t, 5, successCount, "Should have exactly 5 successful requests")
	assert.Equal(t, 5, rateLimitedCount, "Should have exactly 5 rate limited requests")
}

func TestRateLimitMiddleware_RateLimitedResponseFormat(t *testing.T) {
	middleware := NewRateLimitMiddleware(1, time.Second, nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	limitedHandler := middleware.Handler()(handler)

	req := httptest.NewRequest("GET", "/test?key=test-key", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	before := time.Now().UnixMilli()
	req = httptest.NewRequest("GET", "/test?key=test-key", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)
	after := time.Now().UnixMilli()

	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	assert.NotEmpty(t, w.Header().Get("Retry-After"), "Should include Retry-After header")
	assert.Equal(t, "1", w.Header().Get("X-RateLimit-Limit"), "Should include X-RateLimit-Limit header")
	assert.Equal(t, "0", w.Header().Get("X-RateLimit-Remaining"), "Should include X-RateLimit-Remaining header")

	var responseBody map[string]any
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &responseBody))

	assert.Contains(t, responseBody["text"].(string), "Rate limit")

	currentTime := int64(responseBody["currentTime"].(float64))
	assert.GreaterOrEqual(t, currentTime, before)
	assert.LessOrEqual(t, currentTime, after)
}

func TestRateLimitMiddleware_EdgeCases(t *testing.T) {
	t.Run("Zero rate limit", func(t *testing.T) {
		middleware := NewRateLimitMiddleware(0, time.Second, nil)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware.Handler()(handler)

		req := httptest.NewRequest("GET", "/test?key=test-key", nil)
		w := httptest.NewRecorder()

		limitedHandler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusTooManyRequests, w.Code,
			"Zero rate limit should block all requests")
	})

	t.Run("Very high rate limit", func(t *testing.T) {
		middleware := NewRateLimitMiddleware(1000, time.Second, nil)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		limitedHandler := middleware.Handler()(handler)

		for i := 0; i < 100; i++ {
			req := httptest.NewRequest("GET", "/test?key=high-limit-key", nil)
			w := httptest.NewRecorder()

			limitedHandler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code,
				"High rate limit should allow many requests")
		}
	})

	t.Run("Empty API key", func(t *testing.T) {
		middleware := NewRateLimitMiddleware(5, time.Second, nil)

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

func TestRateLimitMiddleware_CorrectRetryAfterTime(t *testing.T) {
	tests := []struct {
		name      string
		rateLimit int
	}{
		{name: "rate limit: 1", rateLimit: 1},
		{name: "rate limit: 2", rateLimit: 2},
		{name: "rate limit: 20", rateLimit: 20},
		{name: "rate limit: 100", rateLimit: 100},
		{name: "rate limit: 200", rateLimit: 200},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			middleware := NewRateLimitMiddleware(testCase.rateLimit, time.Second, nil)

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			limited := middleware.Handler()(handler)

			// Drain the burst and reserve one full burst into the future so the
			// next HTTP request remains rate limited even if CI is slow enough for
			// a few tokens to refill between these calls and ServeHTTP.
			now := time.Now()
			assert.True(t, middleware.limiter.AllowN(now, testCase.rateLimit))
			reservation := middleware.limiter.ReserveN(now, testCase.rateLimit)
			assert.True(t, reservation.OK())

			req := httptest.NewRequest(http.MethodGet, "/test?key=test-key", nil)
			last := httptest.NewRecorder()
			limited.ServeHTTP(last, req)

			assert.Equal(t, http.StatusTooManyRequests, last.Code)

			retryAfterStr := last.Header().Get("Retry-After")
			assert.NotEmpty(t, retryAfterStr)

			retryAfter, err := strconv.Atoi(retryAfterStr)
			assert.NoError(t, err)
			expected := int(math.Ceil(1.0 / float64(testCase.rateLimit)))
			assert.Equal(t, expected, int(retryAfter))
		})
	}

	t.Run("sub-second rate with 2s interval", func(t *testing.T) {
		middleware := NewRateLimitMiddleware(1, 2*time.Second, nil)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		limited := middleware.Handler()(handler)

		now := time.Now()
		assert.True(t, middleware.limiter.AllowN(now, 1))
		reservation := middleware.limiter.ReserveN(now, 1)
		assert.True(t, reservation.OK())

		req := httptest.NewRequest(http.MethodGet, "/test?key=test-key", nil)
		last := httptest.NewRecorder()
		limited.ServeHTTP(last, req)

		assert.Equal(t, http.StatusTooManyRequests, last.Code)

		retryAfterStr := last.Header().Get("Retry-After")
		assert.NotEmpty(t, retryAfterStr)

		retryAfter, err := strconv.Atoi(retryAfterStr)
		assert.NoError(t, err)
		expected := 2
		assert.Equal(t, expected, int(retryAfter))
	})
}
