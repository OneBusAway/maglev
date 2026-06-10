package restapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"maglev.onebusaway.org/internal/models"
)

// VersionValidationMiddleware validates the "version" query parameter on API requests.
// If the parameter is absent or empty, the request is treated as valid (defaults to version 2)
// to maintain backward compatibility with the legacy server and the Wayfinder JS SDK,
// which never sends the version parameter.
// If the parameter is present and does not match models.APIVersion, a 400 Bad Request is returned.
func (api *RestAPI) VersionValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			v := r.URL.Query().Get("version")
			if v != "" && v != strconv.Itoa(models.APIVersion) {
				api.sendError(w, r, http.StatusBadRequest, fmt.Sprintf("unknown version: %s", v))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
