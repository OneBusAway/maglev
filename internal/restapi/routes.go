package restapi

import (
	"net/http"
	"net/http/pprof"
)

type handlerFunc func(w http.ResponseWriter, r *http.Request)

// rateLimitAndValidateAPIKey combines rate limiting, API key validation, and compression
func rateLimitAndValidateAPIKey(api *RestAPI, finalHandler handlerFunc) http.Handler {
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
	mux.Handle("GET /api/where/agencies-with-coverage.json", rateLimitAndValidateAPIKey(api, api.agenciesWithCoverageHandler))
	mux.Handle("GET /api/where/agency/{id}", rateLimitAndValidateAPIKey(api, api.agencyHandler))
	mux.Handle("GET /api/where/current-time.json", rateLimitAndValidateAPIKey(api, api.currentTimeHandler))
	mux.Handle("GET /api/where/routes-for-agency/{id}", rateLimitAndValidateAPIKey(api, api.routesForAgencyHandler))
	mux.Handle("GET /api/where/vehicles-for-agency/{id}", rateLimitAndValidateAPIKey(api, api.vehiclesForAgencyHandler))
	mux.Handle("GET /api/where/stops-for-location.json", rateLimitAndValidateAPIKey(api, api.stopsForLocationHandler))
	mux.Handle("GET /api/where/stop-ids-for-agency/{id}", rateLimitAndValidateAPIKey(api, api.stopIDsForAgencyHandler))
	mux.Handle("GET /api/where/stops-for-agency/{id}", rateLimitAndValidateAPIKey(api, api.stopsForAgencyHandler))
	mux.Handle("GET /api/where/report-problem-with-trip/{id}", rateLimitAndValidateAPIKey(api, api.reportProblemWithTripHandler))
	mux.Handle("GET /api/where/report-problem-with-stop/{id}", rateLimitAndValidateAPIKey(api, api.reportProblemWithStopHandler))
	mux.Handle("GET /api/where/trip/{id}", rateLimitAndValidateAPIKey(api, api.tripHandler))
	mux.Handle("GET /api/where/route-ids-for-agency/{id}", rateLimitAndValidateAPIKey(api, api.routeIDsForAgencyHandler))
	mux.Handle("GET /api/where/route/{id}", rateLimitAndValidateAPIKey(api, api.routeHandler))
	mux.Handle("GET /api/where/stop/{id}", rateLimitAndValidateAPIKey(api, api.stopHandler))
	mux.Handle("GET /api/where/shape/{id}", rateLimitAndValidateAPIKey(api, api.shapesHandler))
	mux.Handle("GET /api/where/routes-for-location.json", rateLimitAndValidateAPIKey(api, api.routesForLocationHandler))
	mux.Handle("GET /api/where/stops-for-route/{id}", rateLimitAndValidateAPIKey(api, api.stopsForRouteHandler))
	mux.Handle("GET /api/where/schedule-for-stop/{id}", rateLimitAndValidateAPIKey(api, api.scheduleForStopHandler))
	mux.Handle("GET /api/where/schedule-for-route/{id}", rateLimitAndValidateAPIKey(api, api.scheduleForRouteHandler))
	mux.Handle("GET /api/where/trip-details/{id}", rateLimitAndValidateAPIKey(api, api.tripDetailsHandler))
	mux.Handle("GET /api/where/block/{id}", rateLimitAndValidateAPIKey(api, api.blockHandler))
	mux.Handle("GET /api/where/trip-for-vehicle/{id}", rateLimitAndValidateAPIKey(api, api.tripForVehicleHandler))
	mux.Handle("GET /api/where/trips-for-location.json", rateLimitAndValidateAPIKey(api, api.tripsForLocationHandler))
	mux.Handle("GET /api/where/arrival-and-departure-for-stop/{id}", rateLimitAndValidateAPIKey(api, api.arrivalAndDepartureForStopHandler))
	mux.Handle("GET /api/where/trips-for-route/{id}", rateLimitAndValidateAPIKey(api, api.tripsForRouteHandler))
	mux.Handle("GET /api/where/arrivals-and-departures-for-stop/{id}", rateLimitAndValidateAPIKey(api, api.arrivalsAndDeparturesForStopHandler))
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
