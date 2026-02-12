package restapi

import (
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/internal/metrics"
)

// MetricsHandler returns middleware that records HTTP metrics.
// If m is nil, returns a pass-through middleware that does nothing.
func MetricsHandler(m *metrics.Metrics) func(http.Handler) http.Handler {
	// Return no-op middleware if metrics is nil
	if m == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &metricsResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r)

			// Use r.Pattern for path (Go 1.22+) to avoid cardinality issues
			path := r.Pattern
			if path == "" {
				path = "unmatched"
			}

			duration := time.Since(start).Seconds()
			status := strconv.Itoa(wrapped.statusCode)

			m.HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
			m.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
		})
	}
}

// metricsResponseWriter wraps http.ResponseWriter to capture status code.
type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *metricsResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}
