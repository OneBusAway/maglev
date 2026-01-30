package restapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// TestRateLimitMiddleware_CleanupKeepsActiveClients verifies active users are not deleted.
func TestRateLimitMiddleware_CleanupKeepsActiveClients(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, mockClock)
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
	middleware.mu.RLock()
	client, exists := middleware.limiters["active-user"]
	middleware.mu.RUnlock()
	require.True(t, exists, "Limiter should exist after first request")
	require.NotNil(t, client)

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
	middleware.mu.Lock()
	now := mockClock.Now()
	threshold := 10 * time.Minute
	for key, client := range middleware.limiters {
		if !middleware.exemptKeys[key] {
			if now.Sub(client.lastSeen) > threshold {
				delete(middleware.limiters, key)
			}
		}
	}
	middleware.mu.Unlock()

	// Verify limiter still exists (last seen was only 6 minutes ago)
	middleware.mu.RLock()
	_, exists = middleware.limiters["active-user"]
	middleware.mu.RUnlock()
	assert.True(t, exists, "Active user limiter should NOT be deleted")
}

// TestRateLimitMiddleware_CleanupRemovesInactiveClients verifies inactive users are deleted after threshold.
func TestRateLimitMiddleware_CleanupRemovesInactiveClients(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, mockClock)
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
	middleware.mu.RLock()
	_, exists := middleware.limiters["inactive-user"]
	middleware.mu.RUnlock()
	require.True(t, exists, "Limiter should exist after first request")

	// Advance time by 11 minutes (past the 10 minute threshold)
	mockClock.Advance(11 * time.Minute)

	// Trigger cleanup manually
	middleware.mu.Lock()
	now := mockClock.Now()
	threshold := 10 * time.Minute
	for key, client := range middleware.limiters {
		if !middleware.exemptKeys[key] {
			if now.Sub(client.lastSeen) > threshold {
				delete(middleware.limiters, key)
			}
		}
	}
	middleware.mu.Unlock()

	// Verify limiter was deleted
	middleware.mu.RLock()
	_, exists = middleware.limiters["inactive-user"]
	middleware.mu.RUnlock()
	assert.False(t, exists, "Inactive user limiter should be deleted after 10+ minutes")
}

// TestRateLimitMiddleware_CleanupHandlesExhaustedLimiters verifies exhausted limiters are deleted based on time, not token count.
func TestRateLimitMiddleware_CleanupHandlesExhaustedLimiters(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(3, time.Second, mockClock)
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
	middleware.mu.RLock()
	client, exists := middleware.limiters["exhausted-user"]
	middleware.mu.RUnlock()
	require.True(t, exists)
	assert.Less(t, client.limiter.Tokens(), 1.0, "Limiter should be exhausted (less than 1 token)")

	// Advance time by 11 minutes without any new requests
	mockClock.Advance(11 * time.Minute)

	// Trigger cleanup manually
	middleware.mu.Lock()
	now := mockClock.Now()
	threshold := 10 * time.Minute
	for key, client := range middleware.limiters {
		if !middleware.exemptKeys[key] {
			if now.Sub(client.lastSeen) > threshold {
				delete(middleware.limiters, key)
			}
		}
	}
	middleware.mu.Unlock()

	// Verify limiter was deleted despite being exhausted
	middleware.mu.RLock()
	_, exists = middleware.limiters["exhausted-user"]
	middleware.mu.RUnlock()
	assert.False(t, exists, "Exhausted limiter should be deleted based on inactivity, not token count")
}

// TestRateLimitMiddleware_CleanupMemoryLeakPrevention verifies cleanup prevents memory leaks from attack scenarios.
func TestRateLimitMiddleware_CleanupMemoryLeakPrevention(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(5, time.Second, mockClock)
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

	// Verify all 100 limiters exist
	middleware.mu.RLock()
	count := len(middleware.limiters)
	middleware.mu.RUnlock()
	assert.Equal(t, 100, count, "Should have 100 attack limiters")

	// Advance time by 11 minutes (attack stops)
	mockClock.Advance(11 * time.Minute)

	// Trigger cleanup manually
	middleware.mu.Lock()
	now := mockClock.Now()
	threshold := 10 * time.Minute
	for key, client := range middleware.limiters {
		if !middleware.exemptKeys[key] {
			if now.Sub(client.lastSeen) > threshold {
				delete(middleware.limiters, key)
			}
		}
	}
	middleware.mu.Unlock()

	// Verify all attack limiters were cleaned up
	middleware.mu.RLock()
	count = len(middleware.limiters)
	middleware.mu.RUnlock()
	assert.Equal(t, 0, count, "All inactive attack limiters should be deleted")
}

// TestRateLimitMiddleware_CleanupPreservesExemptedKeys verifies exempted keys are never deleted.
func TestRateLimitMiddleware_CleanupPreservesExemptedKeys(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, mockClock)
	defer middleware.Stop()

	// Manually add an exempted key to the limiters map
	middleware.mu.Lock()
	middleware.limiters["org.onebusaway.iphone"] = &rateLimitClient{
		limiter:  nil, // Not needed for this test
		lastSeen: mockClock.Now().Add(-20 * time.Minute), // Very old
	}
	middleware.mu.Unlock()

	// Advance time
	mockClock.Advance(1 * time.Minute)

	// Trigger cleanup manually
	middleware.mu.Lock()
	now := mockClock.Now()
	threshold := 10 * time.Minute
	for key, client := range middleware.limiters {
		if !middleware.exemptKeys[key] {
			if now.Sub(client.lastSeen) > threshold {
				delete(middleware.limiters, key)
			}
		}
	}
	middleware.mu.Unlock()

	// Verify exempted key still exists despite being very old
	middleware.mu.RLock()
	_, exists := middleware.limiters["org.onebusaway.iphone"]
	middleware.mu.RUnlock()
	assert.True(t, exists, "Exempted key should never be deleted")
}

// TestRateLimitMiddleware_LastSeenUpdateOnEveryRequest verifies lastSeen timestamp is updated on each request.
func TestRateLimitMiddleware_LastSeenUpdateOnEveryRequest(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, mockClock)
	defer middleware.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limitedHandler := middleware.Handler()(handler)

	// First request at T=0
	req := httptest.NewRequest("GET", "/test?key=timestamp-test", nil)
	w := httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	middleware.mu.RLock()
	firstSeen := middleware.limiters["timestamp-test"].lastSeen
	middleware.mu.RUnlock()

	// Advance time by 2 minutes
	mockClock.Advance(2 * time.Minute)

	// Second request at T=2min
	req = httptest.NewRequest("GET", "/test?key=timestamp-test", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	middleware.mu.RLock()
	secondSeen := middleware.limiters["timestamp-test"].lastSeen
	middleware.mu.RUnlock()

	// Verify lastSeen was updated
	assert.True(t, secondSeen.After(firstSeen), "lastSeen should be updated on subsequent requests")
	assert.Equal(t, 2*time.Minute, secondSeen.Sub(firstSeen), "lastSeen should reflect the 2 minute advancement")
}
