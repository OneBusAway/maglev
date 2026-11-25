package restapi

import (
	"net/http"
)

// WithSecurityHeaders wraps the given handler with security headers middleware
func (api *RestAPI) WithSecurityHeaders(handler http.Handler) http.Handler {
	return securityHeaders(handler)
}

// securityHeaders adds essential security headers to all HTTP responses
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent MIME type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent clickjacking attacks
		w.Header().Set("X-Frame-Options", "DENY")

		// Force HTTPS in production (browser will refuse HTTP connections)
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Prevent XSS attacks
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Control referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy - restrictive but allows API responses and web UI
		csp := "default-src 'none'; frame-ancestors 'none';"
		// Allow inline styles and images from same origin for web UI pages
		if r.URL.Path == "/" || r.URL.Path == "/debug/" {
			csp = "default-src 'self'; style-src 'unsafe-inline'; img-src 'self'; frame-ancestors 'none';"
		}
		w.Header().Set("Content-Security-Policy", csp)

		// Allow-Origin is sent whether or not the request has an Origin header,
		// as Java does: responses are publicly cacheable, so a shared cache can
		// serve a copy fetched without Origin to a browser.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Handle preflight OPTIONS requests. Browsers read the other CORS
		// headers only on a preflight response, so, like Java, only it
		// carries them.
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
