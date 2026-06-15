package restapi

import "net/http"

// SetupAPIRoutes creates and configures the API router with all middleware applied globally.
// It is a test-only helper that mirrors the middleware chain assembled in production by
// cmd/api.CreateServer, so handler tests exercise the same global middleware as production.
func (api *RestAPI) SetupAPIRoutes() http.Handler {
	// Create the base router
	mux := http.NewServeMux()

	// Register all API routes
	api.SetRoutes(mux)

	// Apply global middleware chain: compression -> freshness -> version -> expiry -> base routes
	var handler http.Handler = mux
	handler = GtfsExpiryMiddleware(api.GtfsManager)(handler)
	handler = api.VersionValidationMiddleware(handler)
	handler = api.FreshnessMiddleware(handler)
	handler = CompressionMiddleware(handler)

	return handler
}
