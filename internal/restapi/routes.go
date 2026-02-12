package restapi

import (
	"net/http"
	"net/http/pprof"
)

// rateLimitAndValidateAPIKey combines rate limiting, API key validation, and compression
func rateLimitAndValidateAPIKey(api *RestAPI, finalHandler http.HandlerFunc) http.Handler {
	// Create the handler chain: API key validation -> rate limiting -> compression -> final handler
	finalHandlerHttp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHandler(w, r)
	})

	// Apply compression first (innermost)
	compressedHandler := CompressionMiddleware(finalHandlerHttp)

	// Then rate limiting - use the shared rate limiter instance
	var rateLimitedHandler http.Handler
	if api.rateLimiter != nil {
		rateLimitedHandler = api.rateLimiter.Handler()(compressedHandler)
	} else {
		// Fallback for tests that don't use NewRestAPI constructor
		rateLimitedHandler = compressedHandler
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First validate API key
		if api.RequestHasInvalidAPIKey(r) {
			api.invalidAPIKeyResponse(w, r)
			return
		}
		// Then apply rate limiting and compression
		rateLimitedHandler.ServeHTTP(w, r)
	})
}

// withID applies "Simple ID" validation (just checks regex/length)
func withID(api *RestAPI, handler http.HandlerFunc) http.Handler {
	// Apply ID Middleware -> Then standard rate limits/auth
	return rateLimitAndValidateAPIKey(api, api.ValidateIDMiddleware(handler))
}

// withCombinedID applies "Combined ID" validation (checks for agency_id format)
func withCombinedID(api *RestAPI, handler http.HandlerFunc) http.Handler {
	// Apply Combined ID Middleware -> Then standard rate limits/auth
	return rateLimitAndValidateAPIKey(api, api.ValidateCombinedIDMiddleware(handler))
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

	mux.Handle("GET /api/where/agencies-with-coverage.json", rateLimitAndValidateAPIKey(api, api.agenciesWithCoverageHandler))
	mux.Handle("GET /api/where/current-time.json", rateLimitAndValidateAPIKey(api, api.currentTimeHandler))
	mux.Handle("GET /api/where/stops-for-location.json", rateLimitAndValidateAPIKey(api, api.stopsForLocationHandler))
	mux.Handle("GET /api/where/routes-for-location.json", rateLimitAndValidateAPIKey(api, api.routesForLocationHandler))
	mux.Handle("GET /api/where/trips-for-location.json", rateLimitAndValidateAPIKey(api, api.tripsForLocationHandler))
	mux.Handle("GET /api/where/search/stop.json", rateLimitAndValidateAPIKey(api, api.searchStopsHandler))
	mux.Handle("GET /api/where/search/route.json", rateLimitAndValidateAPIKey(api, api.routeSearchHandler))

	mux.Handle("GET /api/where/agency/{id}", withID(api, api.agencyHandler))
	mux.Handle("GET /api/where/routes-for-agency/{id}", withID(api, api.routesForAgencyHandler))
	mux.Handle("GET /api/where/vehicles-for-agency/{id}", withID(api, api.vehiclesForAgencyHandler))
	mux.Handle("GET /api/where/stop-ids-for-agency/{id}", withID(api, api.stopIDsForAgencyHandler))
	mux.Handle("GET /api/where/stops-for-agency/{id}", withID(api, api.stopsForAgencyHandler))
	mux.Handle("GET /api/where/route-ids-for-agency/{id}", withID(api, api.routeIDsForAgencyHandler))

	mux.Handle("GET /api/where/report-problem-with-trip/{id}", withCombinedID(api, api.reportProblemWithTripHandler))
	mux.Handle("GET /api/where/report-problem-with-stop/{id}", withCombinedID(api, api.reportProblemWithStopHandler))
	mux.Handle("GET /api/where/trip/{id}", withCombinedID(api, api.tripHandler))
	mux.Handle("GET /api/where/route/{id}", withCombinedID(api, api.routeHandler))
	mux.Handle("GET /api/where/stop/{id}", withCombinedID(api, api.stopHandler))
	mux.Handle("GET /api/where/shape/{id}", withCombinedID(api, api.shapesHandler))
	mux.Handle("GET /api/where/stops-for-route/{id}", withCombinedID(api, api.stopsForRouteHandler))
	mux.Handle("GET /api/where/schedule-for-stop/{id}", withCombinedID(api, api.scheduleForStopHandler))
	mux.Handle("GET /api/where/schedule-for-route/{id}", withCombinedID(api, api.scheduleForRouteHandler))
	mux.Handle("GET /api/where/trip-details/{id}", withCombinedID(api, api.tripDetailsHandler))
	mux.Handle("GET /api/where/block/{id}", withCombinedID(api, api.blockHandler))
	mux.Handle("GET /api/where/trip-for-vehicle/{id}", withCombinedID(api, api.tripForVehicleHandler))
	mux.Handle("GET /api/where/arrival-and-departure-for-stop/{id}", withCombinedID(api, api.arrivalAndDepartureForStopHandler))
	mux.Handle("GET /api/where/trips-for-route/{id}", withCombinedID(api, api.tripsForRouteHandler))
	mux.Handle("GET /api/where/arrivals-and-departures-for-stop/{id}", withCombinedID(api, api.arrivalsAndDeparturesForStopHandler))
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
