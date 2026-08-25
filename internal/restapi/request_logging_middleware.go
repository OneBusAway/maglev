package restapi

import (
	"log/slog"
	"net/http"
	"time"

	"maglev.onebusaway.org/internal/logging"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// NewRequestLoggingMiddleware creates middleware that logs HTTP requests
func NewRequestLoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			reqID, _ := r.Context().Value(RequestIDKey).(string)
			reqLogger := logger
			if reqID != "" {
				reqLogger = logger.With(slog.String("request_id", reqID))
			}
			// Add logger to context for downstream handlers
			ctx := logging.WithLogger(r.Context(), reqLogger)
			r = r.WithContext(ctx)

			// Wrap response writer to capture status code
			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK, // Default status
			}

			// Call next handler
			next.ServeHTTP(wrapped, r)

			// Log the request
			duration := time.Since(start)

			logging.LogHTTPRequest(logging.ForComponent(ctx, "http_server"),
				r.Method,
				r.URL.Path,
				wrapped.statusCode,
				float64(duration.Nanoseconds())/1e6,
			)
		})
	}
}
