package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

type handlerFunc func(w http.ResponseWriter, r *http.Request)

// rateLimitAndValidateAPIKey combines rate limiting and API key validation
func rateLimitAndValidateAPIKey(api *RestAPI, finalHandler handlerFunc) http.Handler {
	finalHandlerHttp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHandler(w, r)
	})

	// Apply rate limiting directly to the final handler - use the shared rate limiter instance
	var rateLimitedHandler http.Handler
	if api.rateLimiter != nil {
		rateLimitedHandler = api.rateLimiter.Handler()(finalHandlerHttp)
	} else {
		// Fallback for tests that don't use NewRestAPI constructor
		rateLimitedHandler = finalHandlerHttp
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First validate API key
		if api.RequestHasInvalidAPIKey(r) {
			api.invalidAPIKeyResponse(w, r)
			return
		}
		// Then apply rate limiting
		rateLimitedHandler.ServeHTTP(w, r)
	})
}

// etagStatic applies ETag middleware at the innermost handler level.
// By using an unnamed function type, Go allows this to be passed seamlessly into both
// rateLimitAndValidateAPIKey (which expects handlerFunc) and withID (which expects http.HandlerFunc).
func etagStatic(api *RestAPI, handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	getETagFunc := func() string {
		if api.GtfsManager != nil {
			return api.GtfsManager.GetSystemETag() // Safe, lock-protected read
		}
		return ""
	}

	wrapped := ETagMiddleware(getETagFunc)(http.HandlerFunc(handler))

	return func(w http.ResponseWriter, r *http.Request) {
		// Call ServeHTTP cleanly on our pre-built handler
		wrapped.ServeHTTP(w, r)
	}
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

// withProtectedCombinedID applies "Combined ID" validation and requires a protected API key
func withProtectedCombinedID(api *RestAPI, handler http.HandlerFunc) http.Handler {
	innerHandler := handlerFunc(api.ValidateCombinedIDMiddleware(handler))

	finalHandlerHttp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerHandler(w, r)
	})

	// Apply rate limiting directly to the final handler
	var rateLimitedHandler http.Handler
	if api.rateLimiter != nil {
		rateLimitedHandler = api.rateLimiter.Handler()(finalHandlerHttp)
	} else {
		rateLimitedHandler = finalHandlerHttp
	}

	// Auth check outermost, matching rateLimitAndValidateAPIKey pattern
	return api.validateProtectedAPIKey(rateLimitedHandler)
}

// SetRoutes registers all API endpoints with the provided mux
func (api *RestAPI) SetRoutes(mux *http.ServeMux) {
	// Health check endpoint - no authentication required
	mux.HandleFunc("GET /healthz", api.healthHandler)

	// --- Routes without ID validation ---
	mux.Handle("GET /api/where/agencies-with-coverage.json", CacheControlMiddleware(models.CacheDurationLong, rateLimitAndValidateAPIKey(api, etagStatic(api, api.agenciesWithCoverageHandler))))
	mux.Handle("GET /api/where/search/stop.json", CacheControlMiddleware(models.CacheDurationLong, rateLimitAndValidateAPIKey(api, etagStatic(api, api.searchStopsHandler))))
	mux.Handle("GET /api/where/search/route.json", CacheControlMiddleware(models.CacheDurationLong, rateLimitAndValidateAPIKey(api, etagStatic(api, api.routeSearchHandler))))

	// Non-static endpoints (no ETag)
	mux.Handle("GET /api/where/current-time.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.currentTimeHandler)))
	mux.Handle("GET /api/where/stops-for-location.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.stopsForLocationHandler)))
	mux.Handle("GET /api/where/routes-for-location.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.routesForLocationHandler)))
	mux.Handle("GET /api/where/trips-for-location.json", CacheControlMiddleware(models.CacheDurationShort, rateLimitAndValidateAPIKey(api, api.tripsForLocationHandler)))
	mux.Handle("GET /api/where/config.json", rateLimitAndValidateAPIKey(api, api.configHandler))

	// --- Routes with simple ID validation (agency IDs) ---
	mux.Handle("GET /api/where/agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, etagStatic(api, api.agencyHandler))))
	mux.Handle("GET /api/where/routes-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, etagStatic(api, api.routesForAgencyHandler))))
	mux.Handle("GET /api/where/stop-ids-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, etagStatic(api, api.stopIDsForAgencyHandler))))
	mux.Handle("GET /api/where/stops-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, etagStatic(api, api.stopsForAgencyHandler))))
	mux.Handle("GET /api/where/route-ids-for-agency/{id}", CacheControlMiddleware(models.CacheDurationLong, withID(api, etagStatic(api, api.routeIDsForAgencyHandler))))

	// Real-time simple ID endpoints (no ETag)
	mux.Handle("GET /api/where/vehicles-for-agency/{id}", CacheControlMiddleware(models.CacheDurationShort, withID(api, api.vehiclesForAgencyHandler)))

	// --- Routes with combined ID validation (agency_id_code format) ---
	mux.Handle("GET /api/where/trip/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.tripHandler))))
	mux.Handle("GET /api/where/route/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.routeHandler))))
	mux.Handle("GET /api/where/stop/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.stopHandler))))
	mux.Handle("GET /api/where/shape/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.shapesHandler))))
	mux.Handle("GET /api/where/stops-for-route/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.stopsForRouteHandler))))
	mux.Handle("GET /api/where/schedule-for-stop/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.scheduleForStopHandler))))
	mux.Handle("GET /api/where/schedule-for-route/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.scheduleForRouteHandler))))
	mux.Handle("GET /api/where/block/{id}", CacheControlMiddleware(models.CacheDurationLong, withCombinedID(api, etagStatic(api, api.blockHandler))))

	// Real-time or transactional combined ID endpoints (no ETag)
	mux.Handle("GET /api/where/report-problem-with-trip/{id}", CacheControlMiddleware(models.CacheDurationNone, withCombinedID(api, api.reportProblemWithTripHandler)))
	mux.Handle("GET /api/where/report-problem-with-stop/{id}", CacheControlMiddleware(models.CacheDurationNone, withCombinedID(api, api.reportProblemWithStopHandler)))
	mux.Handle("GET /api/where/problem-reports-for-trip/{id}", CacheControlMiddleware(models.CacheDurationNone, withProtectedCombinedID(api, api.problemReportsForTripHandler)))
	mux.Handle("GET /api/where/problem-reports-for-stop/{id}", CacheControlMiddleware(models.CacheDurationNone, withProtectedCombinedID(api, api.problemReportsForStopHandler)))
	mux.Handle("GET /api/where/trip-details/{id}", CacheControlMiddleware(models.CacheDurationShort, withCombinedID(api, api.tripDetailsHandler)))
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

	// Apply global middleware chain: expiry -> compression -> base routes
	// This ensures all responses are compressed & have expiry headers
	var handler http.Handler = mux
	handler = GtfsExpiryMiddleware(api.GtfsManager)(handler)
	return CompressionMiddleware(handler)
}
