package restapi

import (
	"net/http"
	"time"
)

// FreshnessMiddleware injects the X-Data-Last-Updated header into the response.
func (api *RestAPI) FreshnessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.GtfsManager != nil {
			lastUpdated := api.GtfsManager.GetStaticLastUpdated(r.Context())
			if !lastUpdated.IsZero() {
				// Format as RFC3339 for standard API time representation
				w.Header().Set("X-Data-Last-Updated", lastUpdated.Format(time.RFC3339))
			}
		}
		next.ServeHTTP(w, r)
	})
}
