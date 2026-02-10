package restapi

import (
	"net/http"
	"net/http/httptest"
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
	})

	t.Run("should preserve existing request ID", func(t *testing.T) {
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
}
