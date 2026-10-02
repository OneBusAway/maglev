package restapi

import (
	"time"

	"maglev.onebusaway.org/internal/app"
)

type RestAPI struct {
	*app.Application
	rateLimiter *RateLimitMiddleware
	// blockTripCache holds static per-trip block data between requests. Its zero
	// value works, so the tests that build a RestAPI literal need no change.
	blockTripCache blockTripDataCache
}

// NewRestAPI creates a new RestAPI instance with initialized rate limiter
func NewRestAPI(app *app.Application) *RestAPI {
	return &RestAPI{
		Application: app,
		rateLimiter: NewRateLimitMiddleware(app.Config.RateLimit, time.Second, app.Config.ExemptApiKeys),
	}
}

// Shutdown gracefully stops the RestAPI resources
func (api *RestAPI) Shutdown() {}
