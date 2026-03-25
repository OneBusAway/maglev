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

// TestRateLimitMiddleware_LazyEvictionKeepsActiveClients verifies that an active
// user's limiter is reused (not reset) as long as they remain within the idle threshold.
func TestRateLimitMiddleware_LazyEvictionKeepsActiveClients(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, nil, mockClock)
	defer middleware.Stop()

	// create a limiter
	limiter1 := middleware.getLimiter("active-user")
	require.NotNil(t, limiter1)

	// advance time, but stay within threshold
	mockClock.Advance(5 * time.Minute)

	// fetch the limiter again should be the same instance (not lazily evicted)
	limiter2 := middleware.getLimiter("active-user")
	assert.Same(t, limiter1, limiter2,
		"Active user should receive the same limiter instance")
}

// TestRateLimitMiddleware_LazyEvictionResetsIdleClients verifies that an idle
// user's limiter is transparently replaced with a fresh one on access.
func TestRateLimitMiddleware_LazyEvictionResetsIdleClients(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(10, time.Second, nil, mockClock)
	defer middleware.Stop()

	// create a limiter
	limiter1 := middleware.getLimiter("idle-user")
	require.NotNil(t, limiter1)

	// advance time past the idle threshold (>10 min)
	mockClock.Advance(11 * time.Minute)

	// fetch the limiter again should be a new instance (lazy eviction)
	limiter2 := middleware.getLimiter("idle-user")
	assert.NotSame(t, limiter1, limiter2,
		"Idle user should receive a fresh limiter after the threshold")
}

// TestRateLimitMiddleware_LazyEvictionResetsExhaustedLimiters verifies that
// an exhausted, idle limiter is replaced with a fresh one upon the next request.
func TestRateLimitMiddleware_LazyEvictionResetsExhaustedLimiters(t *testing.T) {
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

	// Capture the exhausted limiter
	middleware.mu.Lock()
	client, ok := middleware.limiters.Get("exhausted-user")
	middleware.mu.Unlock()
	require.True(t, ok)
	exhaustedLimiter := client.limiter
	assert.Less(t, exhaustedLimiter.Tokens(), 1.0,
		"Limiter should be exhausted (less than 1 token)")

	// Advance time past the idle threshold
	mockClock.Advance(11 * time.Minute)

	// Next request should succeed, the limiter was lazily replaced
	req = httptest.NewRequest("GET", "/test?key=exhausted-user", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code,
		"Request after lazy eviction of exhausted limiter should succeed")

	// Verify a new limiter was created
	middleware.mu.Lock()
	client, ok = middleware.limiters.Get("exhausted-user")
	middleware.mu.Unlock()
	require.True(t, ok)
	assert.NotSame(t, exhaustedLimiter, client.limiter,
		"Should have received a fresh limiter after idle threshold")
}

// TestRateLimitMiddleware_LRUEviction verifies that the LRU cache evicts
// the least-recently-used entries when it exceeds its capacity.
func TestRateLimitMiddleware_LRUEviction(t *testing.T) {
	mockClock := clock.NewMockClock(time.Now())
	middleware := NewRateLimitMiddleware(5, time.Second, nil, mockClock)
	defer middleware.Stop()

	for i := 0; i < maxLRUSize; i++ {
		middleware.getLimiter(fmt.Sprintf("key-%d", i))
	}
	middleware.mu.Lock()
	assert.Equal(t, maxLRUSize, middleware.limiters.Len(),
		"Cache should be at capacity")
	middleware.mu.Unlock()

	// Adding one more should evict the oldest (key-0)
	middleware.getLimiter("overflow-key")
	middleware.mu.Lock()
	assert.Equal(t, maxLRUSize, middleware.limiters.Len(),
		"Cache should not exceed capacity")

	// The oldest key should have been evicted
	_, ok := middleware.limiters.Get("key-0")
	assert.False(t, ok, "Least recently used key should be evicted")

	// The newest keys should still exist
	_, ok = middleware.limiters.Get("overflow-key")
	assert.True(t, ok, "Most recently added key should exist")

	_, ok = middleware.limiters.Get(fmt.Sprintf("key-%d", maxLRUSize-1))
	assert.True(t, ok, "Recently used key should still exist")
	middleware.mu.Unlock()
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

	middleware.mu.Lock()
	client, ok := middleware.limiters.Get("timestamp-test")
	middleware.mu.Unlock()
	require.True(t, ok)
	firstSeenNano := client.lastSeen.Load()
	firstSeen := time.Unix(0, firstSeenNano)

	// Advance time by 2 minutes
	mockClock.Advance(2 * time.Minute)

	// Second request at T=2min
	req = httptest.NewRequest("GET", "/test?key=timestamp-test", nil)
	w = httptest.NewRecorder()
	limitedHandler.ServeHTTP(w, req)

	middleware.mu.Lock()
	client, ok = middleware.limiters.Get("timestamp-test")
	middleware.mu.Unlock()
	require.True(t, ok)
	secondSeenNano := client.lastSeen.Load()
	secondSeen := time.Unix(0, secondSeenNano)

	// Verify lastSeen was updated
	assert.True(t, secondSeen.After(firstSeen), "lastSeen should be updated on subsequent requests")
	assert.Equal(t, 2*time.Minute, secondSeen.Sub(firstSeen), "lastSeen should reflect the 2 minute advancement")
}

// TestRateLimitMiddleware_ConcurrentGetLimiterReturnsSameInstance verifies
// that concurrent calls to getLimiter for the same key all receive a valid limiter.
func TestRateLimitMiddleware_ConcurrentGetLimiterReturnsSameInstance(t *testing.T) {
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

	// All goroutines should get a valid limiter and the key should exist
	for i := 1; i < goroutines; i++ {
		assert.Same(t, limiters[0], limiters[i], "All goroutines should get the same limiter instance")
	}

	middleware.mu.Lock()
	_, ok := middleware.limiters.Get("new-key")
	middleware.mu.Unlock()
	assert.True(t, ok, "Key should exist in cache after concurrent access")
}
