package restapi

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/internal/logging"

	"golang.org/x/time/rate"
)

// RateLimitMiddleware provides global rate limiting with optional per-key exemptions.
type RateLimitMiddleware struct {
	limiter    *rate.Limiter
	rateLimit  rate.Limit
	burstSize  int
	exemptKeys map[string]bool
}

// NewRateLimitMiddleware creates a new rate limiting middleware.
// ratePerSecond: number of requests allowed per second (0 blocks all, negative is unlimited)
// burstSize: equal to ratePerSecond
func NewRateLimitMiddleware(ratePerSecond int, interval time.Duration, exemptKeys []string) *RateLimitMiddleware {
	var rateLimit rate.Limit
	switch {
	case ratePerSecond < 0:
		rateLimit = rate.Inf
	case ratePerSecond == 0:
		rateLimit = 0
	default:
		rateLimit = rate.Every(interval / time.Duration(ratePerSecond))
	}

	// Clamp burst to 0 for the unlimited case so burstSize is never negative.
	burst := max(ratePerSecond, 0)

	exemptMap := make(map[string]bool)
	for _, key := range exemptKeys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey != "" {
			exemptMap[trimmedKey] = true
		}
	}

	return &RateLimitMiddleware{
		limiter:    rate.NewLimiter(rateLimit, burst),
		rateLimit:  rateLimit,
		burstSize:  burst,
		exemptKeys: exemptMap,
	}
}

// Handler returns the HTTP middleware handler function
func (rl *RateLimitMiddleware) Handler() func(http.Handler) http.Handler {
	return rl.rateLimitHandler
}

func (rl *RateLimitMiddleware) rateLimitHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.URL.Query().Get("key")

		if rl.exemptKeys[apiKey] {
			next.ServeHTTP(w, r)
			return
		}

		if !rl.limiter.Allow() {
			rl.sendRateLimitExceeded(w)
			return
		}

		if rl.rateLimit != rate.Inf {
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.burstSize))
			remaining := int(math.Floor(rl.limiter.Tokens()))
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		}

		next.ServeHTTP(w, r)
	})
}

// sendRateLimitExceeded sends a 429 Too Many Requests response
func (rl *RateLimitMiddleware) sendRateLimitExceeded(w http.ResponseWriter) {
	var retryAfter time.Duration
	switch rl.rateLimit {
	case 0:
		retryAfter = time.Hour // suggest retrying much later when all requests are blocked
	case rate.Inf:
		retryAfter = time.Second // should not happen, but fallback
	default:
		retryAfter = time.Duration(float64(time.Second) / float64(rl.rateLimit))
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.burstSize))
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.WriteHeader(http.StatusTooManyRequests)

	errorResponse := map[string]any{
		"code": http.StatusTooManyRequests,
		"text": "Rate limit exceeded. Please try again later.",
		"data": map[string]any{
			"entry": nil,
			"references": map[string]any{
				"agencies":  []any{},
				"routes":    []any{},
				"stops":     []any{},
				"trips":     []any{},
				"stopTimes": []any{},
			},
		},
		"currentTime": time.Now().UnixMilli(),
		"version":     2,
	}

	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		logger := slog.Default().With(slog.String("component", "rate_limit_middleware"))
		logging.LogError(logger, "failed to encode rate limit response", err)
	}
}
