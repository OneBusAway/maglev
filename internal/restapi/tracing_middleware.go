package restapi

import (
	"net/http"
	"net/url"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracingMiddleware wraps an HTTP handler to create OpenTelemetry spans for each request.
// It captures HTTP method, URL, status code, and records errors.
// URLs are sanitized to prevent leaking API keys and other sensitive query parameters.
func TracingMiddleware(next http.Handler) http.Handler {
	tracer := otel.Tracer("maglev.http")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract span context from incoming request headers (for distributed tracing)
		ctx := r.Context()

		// Use route pattern for span name to avoid high-cardinality
		// Fall back to path if pattern is not available
		spanName := r.URL.Path
		if pattern := r.Pattern; pattern != "" {
			spanName = pattern
		}

		// Sanitize URL to prevent leaking API keys
		sanitizedURL := sanitizeURL(r.URL)

		// Start a new span for this HTTP request
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", sanitizedURL),
				attribute.String("http.scheme", r.URL.Scheme),
				attribute.String("http.host", r.Host),
				attribute.String("http.user_agent", r.UserAgent()),
			),
		)
		defer span.End()

		// Wrap the ResponseWriter to capture the status code
		wrapper := &statusCapturingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default to 200
		}

		// Pass the context with the span to the next handler
		next.ServeHTTP(wrapper, r.WithContext(ctx))

		// Record the HTTP status code
		span.SetAttributes(attribute.Int("http.status_code", wrapper.statusCode))

		// Mark span as error if status code is 5xx
		if wrapper.statusCode >= 500 {
			span.SetStatus(codes.Error, http.StatusText(wrapper.statusCode))
		}
	})
}

// statusCapturingResponseWriter wraps http.ResponseWriter to capture the status code
type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and forwards to the underlying writer
func (w *statusCapturingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write ensures WriteHeader is called with the default status if not already called
func (w *statusCapturingResponseWriter) Write(b []byte) (int, error) {
	// WriteHeader is called automatically by http package if not already called
	return w.ResponseWriter.Write(b)
}

// sanitizeURL removes sensitive query parameters (like API keys) from URLs
// to prevent leaking credentials into traces and logs.
func sanitizeURL(u *url.URL) string {
	if u.RawQuery == "" {
		return u.Path
	}

	// Parse query parameters
	query := u.Query()

	// Remove sensitive parameters
	query.Del("key")
	query.Del("api_key")
	query.Del("apikey")
	query.Del("token")
	query.Del("access_token")

	// If no query parameters remain, return just the path
	if len(query) == 0 {
		return u.Path
	}

	// Return path with sanitized query string
	return u.Path + "?" + query.Encode()
}
