package restapi

import (
	"maglev.onebusaway.org/internal/logging"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestIDMiddleware(t *testing.T) {
	t.Run("should generate request ID if missing", func(t *testing.T) {
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID, ok := r.Context().Value(RequestIDKey).(string)
			assert.True(t, ok, "Context should contain request ID")
			assert.NotEmpty(t, reqID, "Request ID should not be empty")
		})

		handlerToTest := RequestIDMiddleware(nextHandler)

		req := httptest.NewRequest("GET", "http://example.com/foo", nil)
		rec := httptest.NewRecorder()

		handlerToTest.ServeHTTP(rec, req)

		respID := rec.Header().Get("X-Request-ID")
		assert.NotEmpty(t, respID, "Response header should contain X-Request-ID")
		assert.Regexp(t, `^[0-9a-f-]{36}$`, respID)
	})

	t.Run("should preserve existing valid request ID", func(t *testing.T) {
		existingID := "my-custom-trace-id-123"

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID, ok := r.Context().Value(RequestIDKey).(string)
			assert.True(t, ok)
			assert.Equal(t, existingID, reqID)
		})

		handlerToTest := RequestIDMiddleware(nextHandler)

		req := httptest.NewRequest("GET", "http://example.com/foo", nil)
		req.Header.Set("X-Request-ID", existingID)
		rec := httptest.NewRecorder()

		handlerToTest.ServeHTTP(rec, req)

		assert.Equal(t, existingID, rec.Header().Get("X-Request-ID"))
	})

	t.Run("should preserve exactly 128 character request ID (boundary)", func(t *testing.T) {
		existingID := strings.Repeat("a", 128)

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID, ok := r.Context().Value(RequestIDKey).(string)
			assert.True(t, ok)
			assert.Equal(t, existingID, reqID)
		})

		handlerToTest := RequestIDMiddleware(nextHandler)

		req := httptest.NewRequest("GET", "http://example.com/foo", nil)
		req.Header.Set("X-Request-ID", existingID)
		rec := httptest.NewRecorder()

		handlerToTest.ServeHTTP(rec, req)

		assert.Equal(t, existingID, rec.Header().Get("X-Request-ID"))
	})

	t.Run("should replace invalid request ID", func(t *testing.T) {
		testCases := []struct {
			name      string
			invalidID string
		}{
			{
				name:      "ID too long (>128 chars)",
				invalidID: strings.Repeat("a", 129),
			},
			{
				name:      "ID contains invalid characters",
				invalidID: "bad-id-<script>",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reqID, ok := r.Context().Value(RequestIDKey).(string)
					assert.True(t, ok)
					assert.NotEqual(t, tc.invalidID, reqID)
					assert.Regexp(t, `^[0-9a-f-]{36}$`, reqID)
				})

				handlerToTest := RequestIDMiddleware(nextHandler)

				req := httptest.NewRequest("GET", "http://example.com/foo", nil)
				req.Header.Set("X-Request-ID", tc.invalidID)
				rec := httptest.NewRecorder()

				handlerToTest.ServeHTTP(rec, req)
			})
		}
	})
}

func TestRequestIDLoggingIntegration(t *testing.T) {
	var logBuf bytes.Buffer

	testLogger := logging.NewStructuredLogger(&logBuf, 0)

	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	loggingMiddleware := NewRequestLoggingMiddleware(testLogger)(finalHandler)
	handlerToTest := RequestIDMiddleware(loggingMiddleware)

	expectedReqID := "integration-test-id-999"
	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	req.Header.Set("X-Request-ID", expectedReqID)
	rec := httptest.NewRecorder()

	handlerToTest.ServeHTTP(rec, req)

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, expectedReqID, "Log output should contain the request ID")
	assert.Contains(t, logOutput, "request_id", "Log output should contain the request_id key")
}
