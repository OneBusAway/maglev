package restapi

import (
	"fmt"
	"net/http"
)

// CacheControlMiddleware adds generic HTTP caching headers to the response.
func CacheControlMiddleware(durationSeconds int, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if durationSeconds > 0 {
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", durationSeconds))
		} else {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}

		next.ServeHTTP(w, r)
	})
}
