package restapi

import (
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/gtfs"
)

// GtfsExpiryMiddleware checks if the GTFS static data has expired.
func GtfsExpiryMiddleware(manager *gtfs.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if manager != nil {
				// Only apply this header to API routes to reduce noise on other endpoints
				if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
					expiresAt := manager.FeedExpiresAt()
					if !expiresAt.IsZero() && time.Now().After(expiresAt) {
						w.Header().Set("X-Data-Expired", "true")
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
