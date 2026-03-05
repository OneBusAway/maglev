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
				expiresAt := manager.FeedExpiresAt()
				if !expiresAt.IsZero() && time.Now().After(expiresAt) {
					w.Header().Set("X-Data-Expired", "true")
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
