package restapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	"maglev.onebusaway.org/internal/clock"
)

// TestRateLimitMiddleware_CleanupKeepsActiveClients verifies active users are not deleted.
func TestRateLimitMiddleware_CleanupKeepsActiveClients(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, nil, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limitedHandler := middleware.Handler()(handler)

	// Make a request from a user (creates a limiter)
	req := httptest.NewRequest("GET", "/test?key=active-user", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify limiter exists
	val, exists := middleware.limiters.Load("active-user")
	require.True(t, exists, "Limiter should exist after first request")
	require.NotNil(t, val.(*rateLimitClient))

	// Advance time by 5 minutes (less than 10 minute threshold)
	mockClock.Advance(5 * time.Minute)

	// Make another request (user is still active)
	req = httptest.NewRequest("GET", "/test?key=active-user", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Advance time by another 6 minutes (total 11 minutes since creation, but only 6 since last use)
	mockClock.Advance(6 * time.Minute)

	// Trigger cleanup manually
	middleware.cleanupOnce()

	// Verify limiter still exists (last seen was only 6 minutes ago)
	_, exists = middleware.limiters.Load("active-user")
	assert.True(t, exists, "Active user limiter should NOT be deleted")
}

// TestRateLimitMiddleware_CleanupRemovesInactiveClients verifies inactive users are deleted after threshold.
func TestRateLimitMiddleware_CleanupRemovesInactiveClients(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, nil, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limitedHandler := middleware.Handler()(handler)

	// Make a request from a user (creates a limiter)
	req := httptest.NewRequest("GET", "/test?key=inactive-user", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Verify limiter exists
	_, exists := middleware.limiters.Load("inactive-user")
	require.True(t, exists, "Limiter should exist after first request")

	// Advance time by 11 minutes (past the 10 minute threshold)
	mockClock.Advance(11 * time.Minute)

	// Trigger cleanup manually
	middleware.cleanupOnce()

	// Verify limiter was deleted
	_, exists = middleware.limiters.Load("inactive-user")
	assert.False(t, exists, "Inactive user limiter should be deleted after 10+ minutes")
}

// TestRateLimitMiddleware_CleanupHandlesExhaustedLimiters verifies exhausted limiters are deleted based on time, not token count.
func TestRateLimitMiddleware_CleanupHandlesExhaustedLimiters(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(3, time.Second, nil, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limitedHandler := middleware.Handler()(handler)

	// Exhaust the limiter (3 requests, then rate limited)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test?key=exhausted-user", nil)
		w := httptest.NewRecorder()
		limitedHandler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// 4th request should be rate limited
	req := httptest.NewRequest("GET", "/test?key=exhausted-user", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Verify limiter exists and is effectively exhausted
	val, exists := middleware.limiters.Load("exhausted-user")
	require.True(t, exists)
	client := val.(*rateLimitClient)
	assert.Less(t, client.limiter.Tokens(), 1.0, "Limiter should be exhausted (less than 1 token)")

	// Advance time by 11 minutes without any new requests
	mockClock.Advance(11 * time.Minute)

	// Trigger cleanup manually
	middleware.cleanupOnce()

	// Verify limiter was deleted despite being exhausted
	_, exists = middleware.limiters.Load("exhausted-user")
	assert.False(t, exists, "Exhausted limiter should be deleted based on inactivity, not token count")
}

// TestRateLimitMiddleware_CleanupMemoryLeakPrevention verifies cleanup prevents memory leaks from attack scenarios.
func TestRateLimitMiddleware_CleanupMemoryLeakPrevention(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(5, time.Second, nil, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limitedHandler := middleware.Handler()(handler)

	// Simulate attack: 100 different API keys, each exhausting their limit
	attackKeys := make([]string, 100)
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("attacker-key-%d", i)
		attackKeys[i] = key

		// Exhaust the limiter for this key
		for j := 0; j < 6; j++ {
			req := httptest.NewRequest("GET", fmt.Sprintf("/test?key=%s", key), nil)
			w := httptest.NewRecorder()
			limitedHandler.ServeHTTP(w, req)
		}
	}

	// Verify all 100 limiters exist using sync.Map Range
	count := 0
	middleware.limiters.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 100, count, "Should have 100 attack limiters")

	// Advance time by 11 minutes (attack stops)
	mockClock.Advance(11 * time.Minute)

	// Trigger cleanup manually
	middleware.cleanupOnce()

	// Verify all attack limiters were cleaned up
	count = 0
	middleware.limiters.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count, "All inactive attack limiters should be deleted")
}

// TestRateLimitMiddleware_CleanupPreservesExemptedKeys verifies exempted keys are never deleted.
func TestRateLimitMiddleware_CleanupPreservesExemptedKeys(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, []string{"org.onebusaway.iphone"}, mockClock)
	defer middleware.Stop()

	// Manually add an exempted key to the limiters map
	exemptClient := &rateLimitClient{limiter: nil}
	exemptClient.lastSeen.Store(mockClock.Now().Add(-20 * time.Minute).UnixNano())
	middleware.limiters.Store("org.onebusaway.iphone", exemptClient)

	// Advance time
	mockClock.Advance(1 * time.Minute)

	// Trigger cleanup manually
	middleware.cleanupOnce()

	// Verify exempted key still exists despite being very old
	_, exists := middleware.limiters.Load("org.onebusaway.iphone")
	assert.True(t, exists, "Exempted key should never be deleted")
}

// TestRateLimitMiddleware_LastSeenUpdateOnEveryRequest verifies lastSeen timestamp is updated on each request.
func TestRateLimitMiddleware_LastSeenUpdateOnEveryRequest(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, nil, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limitedHandler := middleware.Handler()(handler)

	// First request at T=0
	req := httptest.NewRequest("GET", "/test?key=timestamp-test", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	val, _ := middleware.limiters.Load("timestamp-test")
	firstSeenNano := val.(*rateLimitClient).lastSeen.Load()
	firstSeen := time.Unix(0, firstSeenNano)

	// Advance time by 2 minutes
	mockClock.Advance(2 * time.Minute)

	// Second request at T=2min
	req = httptest.NewRequest("GET", "/test?key=timestamp-test", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	val, _ = middleware.limiters.Load("timestamp-test")
	secondSeenNano := val.(*rateLimitClient).lastSeen.Load()
	secondSeen := time.Unix(0, secondSeenNano)

	// Verify lastSeen was updated
	assert.True(t, secondSeen.After(firstSeen), "lastSeen should be updated on subsequent requests")
	assert.Equal(t, 2*time.Minute, secondSeen.Sub(firstSeen), "lastSeen should reflect the 2 minute advancement")
}

func TestRateLimitMiddleware_DoubleCheckedLockingCreatesOneLimiter(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(100, time.Second, nil, mockClock)
	defer middleware.Stop()

	const goroutines = 50
	var wg sync.WaitGroup
	limiters := make([]*rate.Limiter, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			limiters[idx] = middleware.getLimiter("new-key")
		}(i)
	}
	wg.Wait()

	// All goroutines should get the exact same limiter instance
	for i := 1; i < goroutines; i++ {
		assert.Same(t, limiters[0], limiters[i],
			"All goroutines should get the same limiter instance")
	}

	count := 0
	middleware.limiters.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 1, count, "Exactly one limiter should be created")
}
