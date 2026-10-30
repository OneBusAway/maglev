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

// ifNoneMatchHits checks every If-None-Match header using weak comparison,
// as required by RFC 9110 section 13.1.2.
func ifNoneMatchHits(r *http.Request, etag string) bool {
	etag = strings.TrimPrefix(etag, "W/")
	for _, value := range r.Header.Values("If-None-Match") {
		value = strings.Trim(value, " \t")
		if value == "*" || entityTagListMatches(value, etag) {
			return true
		}
	}
	return false
}

func entityTagListMatches(value, etag string) bool {
	// Static endpoints currently send an unquoted feed hash. Preserve clients
	// echoing that legacy value without accepting unquoted tags for valid ETags.
	if !strings.HasPrefix(etag, `"`) {
		for _, part := range strings.Split(value, ",") {
			if strings.TrimSpace(part) == etag {
				return etag != ""
			}
		}
		return false
	}
	for value != "" {
		value = strings.TrimLeft(value, " \t,")
		tag, rest := scanEntityTag(value)
		if tag == "" {
			break
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest != "" && rest[0] != ',' {
			break
		}
		if tag == etag {
			return true
		}
		value = rest
	}
	return false
}

// scanEntityTag reads one entity tag, omitting its optional weak prefix.
// Commas and backslashes inside the quoted opaque tag are literal characters.
func scanEntityTag(value string) (tag, rest string) {
	value = strings.TrimPrefix(value, "W/")
	if len(value) < 2 || value[0] != '"' {
		return "", ""
	}
	for i := 1; i < len(value); i++ {
		if value[i] == '"' {
			return value[:i+1], value[i+1:]
		}
		// RFC 9110 section 8.8.3 excludes spaces, controls, and DEL.
		if value[i] < 0x21 || value[i] == 0x7f {
			return "", ""
		}
	}
	return "", ""
}
