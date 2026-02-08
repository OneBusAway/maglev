package restapi

import (
	"time"

	"maglev.onebusaway.org/internal/app"
)

type RestAPI struct {
	*app.Application
	rateLimiter *RateLimitMiddleware
}

// NewRestAPI creates a new RestAPI instance with initialized rate limiter
func NewRestAPI(app *app.Application) *RestAPI {
	return &RestAPI{
		Application: app,
		rateLimiter: NewRateLimitMiddleware(app.Config.RateLimit, time.Second, app.Config.ExemptApiKeys, app.Clock),
	}
}

// Shutdown gracefully stops the RestAPI resources
func (api *RestAPI) Shutdown() {
	if api.rateLimiter != nil {
		api.rateLimiter.Stop()
	}
}
