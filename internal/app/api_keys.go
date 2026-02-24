package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

	hash := sha256.Sum256([]byte(key))
	hashedKeyHex := hex.EncodeToString(hash[:])

	validHashedKeys := app.Config.ApiKeys
	for _, validHashedKey := range validHashedKeys {
		if subtle.ConstantTimeCompare([]byte(hashedKeyHex), []byte(validHashedKey)) == 1 {
			return false
		}
	}

	return true
}
