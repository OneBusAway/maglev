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

	"github.com/hashicorp/golang-lru/v2/simplelru"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/logging"

	"golang.org/x/time/rate"
)

// maxLRUSize is the maximum number of per-key rate limiters held in the cache.
const maxLRUSize = 10_000

// idleThreshold defines how long a key must be idle
const idleThreshold = 10 * time.Minute

// rateLimitClient tracks the limiter and its last usage time.
// This allows us to lazily reset inactive users without disrupting active ones.
type rateLimitClient struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64 // Unix nanoseconds (time.Time.UnixNano())
}

// RateLimitMiddleware provides per-API-key rate limiting using an LRU cache with lazy eviction.
type RateLimitMiddleware struct {
	mu         sync.Mutex
	limiters   *simplelru.LRU[string, *rateLimitClient]
	rateLimit  rate.Limit
	burstSize  int
	exemptKeys map[string]bool
	clock      clock.Clock
}

// NewRateLimitMiddleware creates a new rate limiting middleware.
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

	cache, _ := simplelru.NewLRU[string, *rateLimitClient](maxLRUSize, nil)

	middleware := &RateLimitMiddleware{
		limiters:   cache,
		rateLimit:  rateLimit,
		burstSize:  ratePerSecond,
		exemptKeys: exemptMap,
		clock:      clock,
	}

	return middleware
}

// Handler returns the HTTP middleware handler function
func (rl *RateLimitMiddleware) Handler() func(http.Handler) http.Handler {
	return rl.rateLimitHandler
}

// getLimiter gets or creates a rate limiter for the given API key.
// It implements lazy eviction
func (rl *RateLimitMiddleware) getLimiter(apiKey string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.clock.Now()

	if client, ok := rl.limiters.Get(apiKey); ok {
		lastSeenNano := client.lastSeen.Load()
		lastSeenTime := time.Unix(0, lastSeenNano)

		if now.Sub(lastSeenTime) > idleThreshold {
			// Lazily evict: replace with a fresh limiter
			newLimiter := rate.NewLimiter(rl.rateLimit, rl.burstSize)
			newClient := &rateLimitClient{
				limiter: newLimiter,
			}
			newClient.lastSeen.Store(now.UnixNano())
			rl.limiters.Add(apiKey, newClient)
			return newLimiter
		}

		client.lastSeen.Store(now.UnixNano())
		return client.limiter
	}

	// Key not in cache
	limiter := rate.NewLimiter(rl.rateLimit, rl.burstSize)
	newClient := &rateLimitClient{
		limiter: limiter,
	}
	newClient.lastSeen.Store(now.UnixNano())
	rl.limiters.Add(apiKey, newClient)
	return limiter
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

// Stop performs cleanup of rate limiter resources.
// It is safe to call multiple times with the LRU-based design
func (rl *RateLimitMiddleware) Stop() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.limiters.Purge()
}
