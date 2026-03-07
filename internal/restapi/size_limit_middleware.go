package restapi

import (
	"net/http"
)

// SizeLimitMiddleware wraps the given handler with a maximum request body size limit.
func SizeLimitMiddleware(limitBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limitBytes)
			next.ServeHTTP(w, r)
		})
	}
}
