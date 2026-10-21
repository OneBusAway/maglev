package restapi

import (
	"fmt"
	"net/http"
	"strings"
)

// CacheControlMiddleware adds Cache-Control headers based on the tier
func CacheControlMiddleware(durationSeconds int, next http.Handler) http.Handler {
	var headerValue string
	if durationSeconds > 0 {
		headerValue = fmt.Sprintf("public, max-age=%d", durationSeconds)
	} else {
		headerValue = "no-cache, no-store, must-revalidate"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &cacheControlWriter{
			ResponseWriter: w,
			headerValue:    headerValue,
		}
		next.ServeHTTP(wrapped, r)
	})
}

type cacheControlWriter struct {
	http.ResponseWriter
	headerValue   string
	headerWritten bool
}

func (w *cacheControlWriter) WriteHeader(code int) {
	if !w.headerWritten {
		w.headerWritten = true
		// 304 Not Modified must preserve cache headers
		if (code >= 200 && code < 300) || code == http.StatusNotModified {
			w.ResponseWriter.Header().Set("Cache-Control", w.headerValue)
		} else {
			w.ResponseWriter.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheControlWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// ETagMiddleware handles Conditional HTTP requests by comparing the incoming
// If-None-Match header against the current system ETag.
func ETagMiddleware(getETag func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			etag := getETag(r)
			if etag != "" {
				// RFC 7232: a 304 response MUST include the ETag header
				w.Header().Set("ETag", etag)
				if ifNoneMatchHits(r, etag) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ifNoneMatchHits reports whether the request's If-None-Match header is the
// wildcard or lists etag.
func ifNoneMatchHits(r *http.Request, etag string) bool {
	inm := r.Header.Get("If-None-Match")
	if inm == "*" {
		return true
	}
	for _, part := range strings.Split(inm, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
}
