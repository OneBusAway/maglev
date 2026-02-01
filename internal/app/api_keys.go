package app

import (
	"crypto/subtle"
	"net/http"
)

func (app *Application) RequestHasInvalidAPIKey(r *http.Request) bool {
	key := r.URL.Query().Get("key")
	return app.IsInvalidAPIKey(key)
}

func (app *Application) IsInvalidAPIKey(key string) bool {
	if key == "" {
		return true
	}

	validKeys := app.Config.ApiKeys
	for _, validKey := range validKeys {
		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) == 1 {
			return false
		}
	}

	return true
}
