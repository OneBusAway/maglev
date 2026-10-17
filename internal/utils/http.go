package utils

import (
	"net/http"
	"strings"
)

// ExtractIDFromParams retrieves a parameter value from the request context and removes file extensions like ".json".
func ExtractIDFromParams(r *http.Request) string {
	id := r.PathValue("id")
	return strings.TrimSuffix(id, ".json")
}
