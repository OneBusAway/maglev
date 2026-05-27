package restapi

import (
	"crypto/subtle"
	"net/http"
)

func (api *RestAPI) validateProtectedAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if !isProtectedAPIKey(key, api.Config.ProtectedApiKeys) {
			api.invalidAPIKeyResponse(w)
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
