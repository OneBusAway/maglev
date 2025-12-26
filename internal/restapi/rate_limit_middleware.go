package restapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitMiddleware provides per-API-key rate limiting
type RateLimitMiddleware struct {
	limiters    map[string]*rate.Limiter
	mu          sync.RWMutex
	rateLimit   rate.Limit
	burstSize   int
	cleanupTick *time.Ticker
	exemptKeys  map[string]bool
	stopChan    chan struct{}
	stopOnce    sync.Once
}

// NewRateLimitMiddleware creates a new rate limiting middleware
// ratePerSecond: number of requests allowed per second per API key
// burstSize: number of requests allowed in a burst per API key
func NewRateLimitMiddleware(ratePerSecond int, interval time.Duration) *RateLimitMiddleware {
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

	middleware := &RateLimitMiddleware{
		limiters:    make(map[string]*rate.Limiter),
		rateLimit:   rateLimit,
		burstSize:   ratePerSecond,
		cleanupTick: time.NewTicker(5 * time.Minute), // Cleanup old limiters every 5 minutes
		exemptKeys: map[string]bool{
			"org.onebusaway.iphone": true, // Exempt OneBusAway iPhone app
		},
		stopChan: make(chan struct{}),
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
func (rl *RateLimitMiddleware) getLimiter(apiKey string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[apiKey]
	rl.mu.RUnlock()

	if exists {
		return limiter
	}

	// Create new limiter
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := rl.limiters[apiKey]; exists {
		return limiter
	}

	// Create new limiter with the configured rate and burst
	limiter = rate.NewLimiter(rl.rateLimit, rl.burstSize)
	rl.limiters[apiKey] = limiter

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
		retryAfter = time.Duration(1) / time.Duration(rl.rateLimit)
	}

	// Set headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
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
		"currentTime": time.Now().UnixMilli(),
		"version":     2,
	}

	_ = json.NewEncoder(w).Encode(errorResponse)
}

// cleanup periodically removes old, unused limiters to prevent memory leaks
func (rl *RateLimitMiddleware) cleanup() {
	for {
		select {
		case <-rl.cleanupTick.C:
			rl.mu.Lock()

			// Remove limiters that haven't been used recently
			// For simplicity, we'll remove all limiters and let them be recreated as needed
			// In a production system, you might want to track last access time
			for key := range rl.limiters {
				// Keep exempted keys and recently active limiters
				if !rl.exemptKeys[key] {
					// Simple cleanup: remove limiters that have tokens available (not recently used)
					if limiter := rl.limiters[key]; limiter.Tokens() > 0 {
						delete(rl.limiters, key)
					}
				}
			}

			rl.mu.Unlock()
		case <-rl.stopChan:
			return
		}
	}
}

// Stop stops the cleanup goroutine. It is safe to call multiple times.
func (rl *RateLimitMiddleware) Stop() {
	rl.stopOnce.Do(func() {
		if rl.cleanupTick != nil {
			rl.cleanupTick.Stop()
		}
		close(rl.stopChan)
	})
}
