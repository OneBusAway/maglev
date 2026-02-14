package restapi

import (
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/realtime"
)

type RestAPI struct {
	*app.Application
	rateLimiter     func(http.Handler) http.Handler
	realtimeService *realtime.Service
}

// NewRestAPI creates a new RestAPI instance with initialized rate limiter
func NewRestAPI(app *app.Application) *RestAPI {
	return &RestAPI{
		Application:     app,
		rateLimiter:     NewRateLimitMiddleware(app.Config.RateLimit, time.Second),
		realtimeService: realtime.NewService(app.GtfsManager, realtime.DefaultConfig()),
	}
}
