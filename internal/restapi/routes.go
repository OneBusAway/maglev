package restapi

import (
	"net/http"
	"net/http/pprof"

	"maglev.onebusaway.org/internal/models"
)

type handlerFunc func(w http.ResponseWriter, r *http.Request)

// rateLimitAndValidateAPIKey combines rate limiting, API key validation, tracing, and compression
func rateLimitAndValidateAPIKey(api *RestAPI, finalHandler handlerFunc) http.Handler {
	// Create the handler chain: API key validation -> rate limiting -> tracing -> compression -> final handler
	finalHandlerHttp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHandler(w, r)
	})

	// Apply compression first (innermost)
	compressedHandler := CompressionMiddleware(finalHandlerHttp)

	// Apply tracing middleware (before rate limiting so we trace even rate-limited requests)
	tracedHandler := TracingMiddleware(compressedHandler)

	// Then rate limiting - use the shared rate limiter instance
	var rateLimitedHandler http.Handler
	if api.rateLimiter != nil {
		rateLimitedHandler = api.rateLimiter.Handler()(tracedHandler)
	} else {
		// Fallback for tests that don't use NewRestAPI constructor
		rateLimitedHandler = tracedHandler
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First validate API key
		if api.RequestHasInvalidAPIKey(r) {
			api.invalidAPIKeyResponse(w, r)
			return
		}
		// Then apply rate limiting, tracing, and compression
		rateLimitedHandler.ServeHTTP(w, r)
	})
}

// withID applies "Simple ID" validation (just checks regex/length)
func withID(api *RestAPI, handler http.HandlerFunc) http.Handler {
	// Apply ID Middleware -> Then standard rate limits/auth
	return rateLimitAndValidateAPIKey(api, handlerFunc(api.ValidateIDMiddleware(handler)))
}

// withCombinedID applies "Combined ID" validation (checks for agency_id format)
func withCombinedID(api *RestAPI, handler http.HandlerFunc) http.Handler {
	// Apply Combined ID Middleware -> Then standard rate limits/auth
	return rateLimitAndValidateAPIKey(api, handlerFunc(api.ValidateCombinedIDMiddleware(handler)))
}

func registerPprofHandlers(mux *http.ServeMux) { // nolint:unused
	// Register pprof handlers
	// import "net/http/pprof"
	// Tutorial: https://medium.com/@rahul.fiem/application-performance-optimization-how-to-effectively-analyze-and-optimize-pprof-cpu-profiles-95280b2f5bfb
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

// SetRoutes registers all API endpoints with compression applied per route
func (api *RestAPI) SetRoutes(mux *http.ServeMux) {
	// Health check endpoint - no authentication required
	mux.HandleFunc("GET /healthz", api.healthHandler)

	// Routes without ID validation (no withID wrapper needed)
	mux.Handle("GET /api/where/agencies-with-coverage.json", CacheControlMiddleware(models.CacheDurationLong, rateLimitAndValidateAPIKey(api, api.agenciesWithCoverageHandler)))
	mux.Handle("GET /api/where/current-time.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.currentTimeHandler)))
	mux.Handle("GET /api/where/stops-for-location.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.stopsForLocationHandler)))
	mux.Handle("GET /api/where/routes-for-location.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.routesForLocationHandler)))
	mux.Handle("GET /api/where/trips-for-location.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.tripsForLocationHandler)))
	mux.Handle("GET /api/where/search/stop.json", CacheControlMiddleware(models.CacheDurationLong, rateLimitAndValidateAPIKey(api, api.searchStopsHandler)))
	mux.Handle("GET /api/where/search/route.json", CacheControlMiddleware(models.CacheDurationLong, rateLimitAndValidateAPIKey(api, api.routeSearchHandler)))
	mux.Handle("GET /api/where/config.json", rateLimitAndValidateAPIKey(api, api.configHandler))

	// Routes with simple ID validation (agency IDs)
	mux.Handle("GET /api/where/agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, api.agencyHandler)))
	mux.Handle("GET /api/where/routes-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, api.routesForAgencyHandler)))
	mux.Handle("GET /api/where/vehicles-for-agency/{id}", CacheControlMiddleware(models.CacheDurationShort, withID(api, api.vehiclesForAgencyHandler)))
	mux.Handle("GET /api/where/stop-ids-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, api.stopIDsForAgencyHandler)))
	mux.Handle("GET /api/where/stops-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, api.stopsForAgencyHandler)))
	mux.Handle("GET /api/where/route-ids-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, api.routeIDsForAgencyHandler)))

	// Routes with combined ID validation (agency_id_code format)
	mux.Handle("GET /api/where/report-problem-with-trip/{id}", CacheControlMiddleware(models.CacheDurationNone, withCombinedID(api, api.reportProblemWithTripHandler)))
	mux.Handle("GET /api/where/report-problem-with-stop/{id}", CacheControlMiddleware(models.CacheDurationNone, withCombinedID(api, api.reportProblemWithStopHandler)))
	mux.Handle("GET /api/where/problem-reports-for-trip/{id}", CacheControlMiddleware(models.CacheDurationNone, withCombinedID(api, api.problemReportsForTripHandler)))
	mux.Handle("GET /api/where/problem-reports-for-stop/{id}", CacheControlMiddleware(models.CacheDurationNone, withCombinedID(api, api.problemReportsForStopHandler)))
	mux.Handle("GET /api/where/trip/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.tripHandler)))
	mux.Handle("GET /api/where/route/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.routeHandler)))
	mux.Handle("GET /api/where/stop/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.stopHandler)))
	mux.Handle("GET /api/where/shape/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.shapesHandler)))
	mux.Handle("GET /api/where/stops-for-route/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.stopsForRouteHandler)))
	mux.Handle("GET /api/where/schedule-for-stop/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.scheduleForStopHandler)))
	mux.Handle("GET /api/where/schedule-for-route/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.scheduleForRouteHandler)))
	mux.Handle("GET /api/where/trip-details/{id}", CacheControlMiddleware(models.CacheDurationShort, withCombinedID(api, api.tripDetailsHandler)))
	mux.Handle("GET /api/where/block/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, api.blockHandler)))
	mux.Handle("GET /api/where/trip-for-vehicle/{id}", CacheControlMiddleware(models.CacheDurationShort, withCombinedID(api, api.tripForVehicleHandler)))
	mux.Handle("GET /api/where/arrival-and-departure-for-stop/{id}", CacheControlMiddleware(models.CacheDurationShort, withCombinedID(api, api.arrivalAndDepartureForStopHandler)))
	mux.Handle("GET /api/where/trips-for-route/{id}", CacheControlMiddleware(models.CacheDurationShort, withCombinedID(api, api.tripsForRouteHandler)))
	mux.Handle("GET /api/where/arrivals-and-departures-for-stop/{id}", CacheControlMiddleware(models.CacheDurationShort, withCombinedID(api, api.arrivalsAndDeparturesForStopHandler)))
}

// SetupAPIRoutes creates and configures the API router with all middleware applied globally
func (api *RestAPI) SetupAPIRoutes() http.Handler {
	// Create the base router
	mux := http.NewServeMux()

	// Register all API routes
	api.SetRoutes(mux)

	// Apply global middleware chain: compression -> base routes
	// This ensures all responses are compressed
	return CompressionMiddleware(mux)
}
