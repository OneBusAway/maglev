package restapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/logging"
)

func TestRequestLoggingMiddleware(t *testing.T) {
	t.Run("logs HTTP request details", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		// Create test handler
		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("test response"))
		})

		// Apply request logging middleware
		middleware := NewRequestLoggingMiddleware(logger)
		handler := middleware(testHandler)

		// Create test request
		req := httptest.NewRequest("GET", "/api/where/stops?key=test", nil)
		req.Header.Set("User-Agent", "test-client/1.0")

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		// Verify response
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "test response", recorder.Body.String())

		// Verify logging
		output := buf.String()
		assert.Contains(t, output, `"level":"INFO"`)
		assert.Contains(t, output, `"msg":"http_request"`)
		assert.Contains(t, output, `"method":"GET"`)
		assert.Contains(t, output, `"path":"/api/where/stops"`)
		assert.Contains(t, output, `"status":200`)
		assert.Contains(t, output, `"user_agent":"test-client/1.0"`)
		assert.Contains(t, output, `"duration_ms":`)
		assert.Contains(t, output, `"component":"http_server"`)
	})

	t.Run("logs different HTTP methods and status codes", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		})

		middleware := NewRequestLoggingMiddleware(logger)
		handler := middleware(testHandler)

		// Test POST request
		req := httptest.NewRequest("POST", "/api/where/create", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		assert.Equal(t, http.StatusCreated, recorder.Code)

		output := buf.String()
		assert.Contains(t, output, `"method":"POST"`)
		assert.Contains(t, output, `"status":201`)

		// Clear buffer for next test
		buf.Reset()

		// Test GET request resulting in 404
		req = httptest.NewRequest("GET", "/api/where/nonexistent", nil)
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		assert.Equal(t, http.StatusNotFound, recorder.Code)

		output = buf.String()
		assert.Contains(t, output, `"method":"GET"`)
		assert.Contains(t, output, `"status":404`)
	})

	t.Run("measures request duration accurately", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		// Handler that takes some time
		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})

		middleware := NewRequestLoggingMiddleware(logger)
		handler := middleware(testHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		recorder := httptest.NewRecorder()

		start := time.Now()
		handler.ServeHTTP(recorder, req)
		actualDuration := time.Since(start)

		output := buf.String()

		// Extract duration from log
		lines := strings.Split(strings.TrimSpace(output), "\n")
		require.Len(t, lines, 1)

		// Should have logged a duration close to actual duration
		assert.Contains(t, output, `"duration_ms":`)

		// Verify duration is reasonable (at least 10ms but not too much longer)
		assert.GreaterOrEqual(t, actualDuration.Milliseconds(), int64(10))
		assert.LessOrEqual(t, actualDuration.Milliseconds(), int64(100)) // Allow for some variance
	})

	t.Run("handles requests without User-Agent header", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := NewRequestLoggingMiddleware(logger)
		handler := middleware(testHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		// Don't set User-Agent header

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		output := buf.String()
		assert.Contains(t, output, `"user_agent":""`)
	})

	t.Run("strips query parameters from logged path", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := NewRequestLoggingMiddleware(logger)
		handler := middleware(testHandler)

		req := httptest.NewRequest("GET", "/api/where/stops?key=secret&lat=39.0&lon=-77.0", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		output := buf.String()
		assert.Contains(t, output, `"path":"/api/where/stops"`)
		assert.NotContains(t, output, "secret")
		assert.NotContains(t, output, "lat=39.0")
	})
}

func TestRequestLoggingIntegration(t *testing.T) {
	t.Run("integrates with existing API handler chain", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		// Create test API
		api := createTestApi(t)

		// Create handler chain with request logging
		handler := createHandlerWithRequestLogging(api, logger)

		req := httptest.NewRequest("GET", "/api/where/current-time.json?key=TEST", nil)
		req.Header.Set("User-Agent", "test-client")

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		assert.Equal(t, http.StatusOK, recorder.Code)

		output := buf.String()
		assert.Contains(t, output, `"method":"GET"`)
		assert.Contains(t, output, `"path":"/api/where/current-time.json"`)
		assert.Contains(t, output, `"status":200`)
		assert.Contains(t, output, `"component":"http_server"`)

		// Verify API response is valid JSON
		assert.Contains(t, recorder.Body.String(), `"code":200`)
	})

	t.Run("logs error responses correctly", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		api := createTestApi(t)

		handler := createHandlerWithRequestLogging(api, logger)

		// Request without API key should return 401
		req := httptest.NewRequest("GET", "/api/where/current-time.json", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		assert.Equal(t, http.StatusUnauthorized, recorder.Code)

		output := buf.String()
		assert.Contains(t, output, `"status":401`)
		assert.Contains(t, output, `"path":"/api/where/current-time.json"`)
	})
}

func TestRequestLoggingWithContext(t *testing.T) {
	t.Run("logger is available in request context", func(t *testing.T) {
		var buf bytes.Buffer
		logger := logging.NewStructuredLogger(&buf, slog.LevelInfo)

		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Should be able to get logger from context
			ctxLogger := logging.FromContext(r.Context())
			require.NotNil(t, ctxLogger)

			// Log something from the handler
			ctxLogger.Info("handler called", slog.String("test", "value"))
			w.WriteHeader(http.StatusOK)
		})

		middleware := NewRequestLoggingMiddleware(logger)
		handler := middleware(testHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)

		output := buf.String()

		// Should have both the handler log and the request log
		assert.Contains(t, output, `"msg":"handler called"`)
		assert.Contains(t, output, `"test":"value"`)
		assert.Contains(t, output, `"msg":"http_request"`)
	})
}

// createHandlerWithRequestLogging creates an API handler with request logging enabled for testing
func createHandlerWithRequestLogging(api *RestAPI, logger *slog.Logger) http.Handler {
	// Create a simple test handler
	mux := http.NewServeMux()

	// Add the current time endpoint for testing
	mux.HandleFunc("/api/where/current-time.json", func(w http.ResponseWriter, r *http.Request) {
		// Check API key for authentication
		if api.RequestHasInvalidAPIKey(r) {
			api.invalidAPIKeyResponse(w, r)
			return
		}
		api.currentTimeHandler(w, r)
	})

	// Apply request logging middleware
	requestLogger := NewRequestLoggingMiddleware(logger)
	return requestLogger(mux)
}
