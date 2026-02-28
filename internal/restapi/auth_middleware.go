package restapi

import (
	"crypto/subtle"
	"net/http"
)

func (api *RestAPI) validateProtectedAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			// fallback to query param
			key = r.URL.Query().Get("key")
		}
		if !isProtectedAPIKey(key, api.Config.ProtectedApiKeys) {
			api.sendError(w, r, http.StatusUnauthorized, "Unauthorized: Valid protected API key required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isProtectedAPIKey(key string, validKeys []string) bool {
	if key == "" {
		return false
	}
	for _, k := range validKeys {
		if subtle.ConstantTimeCompare([]byte(key), []byte(k)) == 1 {
			return true
		}
	}
	return false
}
