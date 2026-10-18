package utils

import (
	"net/http"
	"strings"
)

// ExtractIDFromParams retrieves the "id" path value and removes a trailing ".json" extension.
func ExtractIDFromParams(r *http.Request) string {
	id := r.PathValue("id")
	return strings.TrimSuffix(id, ".json")
}
