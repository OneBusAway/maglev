package restapi

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"maglev.onebusaway.org/internal/logging"

	"golang.org/x/time/rate"
	"maglev.onebusaway.org/internal/clock"
)

// rateLimitClient tracks the limiter and its last usage time.
// This allows us to remove inactive users without disrupting active ones.
type rateLimitClient struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64 // Unix nanoseconds (time.Time.UnixNano())
}

// RateLimitMiddleware provides per-API-key rate limiting
type RateLimitMiddleware struct {
	limiters    sync.Map
	rateLimit   rate.Limit
	burstSize   int
	cleanupTick *time.Ticker
	exemptKeys  map[string]bool
	stopChan    chan struct{}
	stopOnce    sync.Once
	clock       clock.Clock
}

// NewRateLimitMiddleware creates a new rate limiting middleware
// ratePerSecond: number of requests allowed per second per API key
// burstSize: number of requests allowed in a burst per API key
func NewRateLimitMiddleware(ratePerSecond int, interval time.Duration, exemptKeys []string, clock clock.Clock) *RateLimitMiddleware {
	// Handle zero rate limit case
	var rateLimit rate.Limit
	if ratePerSecond <= 0 {
		rateLimit = rate.Inf // Infinite rate limit (no limiting)
		if ratePerSecond == 0 {
			rateLimit = 0 // No requests allowed
		}
	} else {
		rateLimit = rate.Every(interval / time.Duration(ratePerSecond))
	}

	exemptMap := make(map[string]bool)
	for _, key := range exemptKeys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey != "" {
			exemptMap[trimmedKey] = true
		}
	}

	middleware := &RateLimitMiddleware{
		rateLimit:   rateLimit,
		burstSize:   ratePerSecond,
		cleanupTick: time.NewTicker(5 * time.Minute), // Cleanup old limiters every 5 minutes
		exemptKeys:  exemptMap,
		stopChan:    make(chan struct{}),
		clock:       clock,
	}

	// Start cleanup goroutine
	go middleware.cleanup()

	return middleware
}

// Handler returns the HTTP middleware handler function
func (rl *RateLimitMiddleware) Handler() func(http.Handler) http.Handler {
	return rl.rateLimitHandler
}

// getLimiter gets or creates a rate limiter for the given API key
// and updates the last usage timestamp.
func (rl *RateLimitMiddleware) getLimiter(apiKey string) *rate.Limiter {
	// Fast path: lock-free read
	if val, ok := rl.limiters.Load(apiKey); ok {
		client := val.(*rateLimitClient)
		client.lastSeen.Store(rl.clock.Now().UnixNano())
		return client.limiter
	}

	// Client does not exist, create it locally
	limiter := rate.NewLimiter(rl.rateLimit, rl.burstSize)
	newClient := &rateLimitClient{
		limiter: limiter,
	}
	newClient.lastSeen.Store(rl.clock.Now().UnixNano())

	// LoadOrStore ensures we don't overwrite if another goroutine just created it
	actual, _ := rl.limiters.LoadOrStore(apiKey, newClient)
	client := actual.(*rateLimitClient)

	// Ensure lastSeen is updated even if we loaded an existing one from another goroutine
	client.lastSeen.Store(rl.clock.Now().UnixNano())
	return client.limiter
}

// rateLimitHandler is the HTTP middleware function
func (rl *RateLimitMiddleware) rateLimitHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract API key from query parameters
		apiKey := r.URL.Query().Get("key")

		// Use a default key for requests without an API key
		if apiKey == "" {
			apiKey = "__no_key__"
		}

		// Check if this API key is exempted from rate limiting
		if rl.exemptKeys[apiKey] {
			next.ServeHTTP(w, r)
			return
		}

		// Get the rate limiter for this API key
		limiter := rl.getLimiter(apiKey)

		// Check if request is allowed
		if !limiter.Allow() {
			rl.sendRateLimitExceeded(w, r)
			return
		}

		// rate limit headers for successful requests
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.burstSize))
		remaining := int(math.Floor(limiter.Tokens()))
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

		// Request is allowed, continue to next handler
		next.ServeHTTP(w, r)
	})
}

// sendRateLimitExceeded sends a 429 Too Many Requests response
func (rl *RateLimitMiddleware) sendRateLimitExceeded(w http.ResponseWriter, r *http.Request) {
	// Calculate retry-after based on rate limit
	var retryAfter time.Duration
	switch rl.rateLimit {
	case 0:
		retryAfter = time.Hour // For zero rate limit, suggest retrying much later
	case rate.Inf:
		retryAfter = time.Second // Should not happen, but fallback
	default:
		retryAfter = time.Duration(float64(time.Second) / float64(rl.rateLimit))
	}

	// Set headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.burstSize))
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.WriteHeader(http.StatusTooManyRequests)

	// Send JSON error response consistent with OneBusAway API format
	errorResponse := map[string]interface{}{
		"code": http.StatusTooManyRequests,
		"text": "Rate limit exceeded. Please try again later.",
		"data": map[string]interface{}{
			"entry": nil,
			"references": map[string]interface{}{
				"agencies":  []interface{}{},
				"routes":    []interface{}{},
				"stops":     []interface{}{},
				"trips":     []interface{}{},
				"stopTimes": []interface{}{},
			},
		},
		"currentTime": rl.clock.Now().UnixMilli(),
		"version":     2,
	}

	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		logger := slog.Default().With(slog.String("component", "rate_limit_middleware"))
		logging.LogError(logger, "failed to encode rate limit response", err)
	}
}

// cleanupOnce performs a single iteration of removing old, unused limiters.
// It is separated from the background loop so tests can trigger it synchronously.
func (rl *RateLimitMiddleware) cleanupOnce() {
	// Define how long a client must be idle before eviction
	threshold := 10 * time.Minute
	now := rl.clock.Now()

	// Iterate over the sync.Map without acquiring a global lock
	rl.limiters.Range(func(key, value interface{}) bool {
		apiKey := key.(string)
		client := value.(*rateLimitClient)

		// Skip exempted keys
		if !rl.exemptKeys[apiKey] {
			// using Time-Based Eviction (LRU)
			lastSeenNano := client.lastSeen.Load()
			if lastSeenNano != 0 {
				lastSeenTime := time.Unix(0, lastSeenNano)
				if now.Sub(lastSeenTime) > threshold {
					// Safe concurrent deletion
					rl.limiters.Delete(apiKey)
				}
			}
		}
		return true // Return true to continue iterating
	})
}

// cleanup periodically removes old, unused limiters to prevent memory leaks
func (rl *RateLimitMiddleware) cleanup() {
	for {
		select {
		case <-rl.cleanupTick.C:
			rl.cleanupOnce()
		case <-rl.stopChan:
			return
		}
	}
}

// Stop stops the cleanup goroutine. It is safe to call multiple times.
// Note: This does not affect in-flight requests - it only stops the
// background cleanup goroutine.
func (rl *RateLimitMiddleware) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stopChan)
		if rl.cleanupTick != nil {
			rl.cleanupTick.Stop()
		}
	})
}
