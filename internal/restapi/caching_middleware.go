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
				inm := r.Header.Get("If-None-Match")
				// Check for wildcard or iterate through parsed comma-separated parts
				if inm != "" {
					if inm == "*" {
						w.Header().Set("ETag", etag)
						w.WriteHeader(http.StatusNotModified)
						return
					}

					parts := strings.Split(inm, ",")
					for _, part := range parts {
						if strings.TrimSpace(part) == etag {
							// RFC 7232: 304 response MUST include the ETag header
							w.Header().Set("ETag", etag)
							w.WriteHeader(http.StatusNotModified)
							return
						}
					}
				}
				// Otherwise, attach the ETag to the response and proceed normally
				w.Header().Set("ETag", etag)
			}
			next.ServeHTTP(w, r)
		})
	}
}
