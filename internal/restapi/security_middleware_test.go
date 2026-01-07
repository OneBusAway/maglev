package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityHeaders(t *testing.T) {
	// Create a simple handler for testing
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	})

	// Wrap with security headers
	secureHandler := securityHeaders(handler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Execute request
	secureHandler.ServeHTTP(rec, req)

	// Check response
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test response", rec.Body.String())

	// Verify security headers are set
	headers := rec.Header()
	assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", headers.Get("X-Frame-Options"))
	assert.Equal(t, "max-age=31536000; includeSubDomains", headers.Get("Strict-Transport-Security"))
	assert.Equal(t, "1; mode=block", headers.Get("X-XSS-Protection"))
	assert.Equal(t, "strict-origin-when-cross-origin", headers.Get("Referrer-Policy"))
	assert.Equal(t, "default-src 'none'; frame-ancestors 'none';", headers.Get("Content-Security-Policy"))
}

func TestSecurityHeadersWithCORS(t *testing.T) {
	// Create a simple handler for testing
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	})

	// Wrap with security headers
	secureHandler := securityHeaders(handler)

	// Create test request with Origin header
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	// Execute request
	secureHandler.ServeHTTP(rec, req)

	// Check response
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify CORS headers are set when Origin is present
	headers := rec.Header()
	assert.Equal(t, "*", headers.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, OPTIONS", headers.Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type, Authorization", headers.Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "86400", headers.Get("Access-Control-Max-Age"))
}

func TestSecurityHeadersOPTIONSRequest(t *testing.T) {
	// Create a handler that should not be called for OPTIONS
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called for OPTIONS request")
	})

	// Wrap with security headers
	secureHandler := securityHeaders(handler)

	// Create OPTIONS request
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	// Execute request
	secureHandler.ServeHTTP(rec, req)

	// Check that OPTIONS is handled by middleware
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify CORS headers are set
	headers := rec.Header()
	assert.Equal(t, "*", headers.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, OPTIONS", headers.Get("Access-Control-Allow-Methods"))
}

func TestWithSecurityHeaders(t *testing.T) {
	// Create a test API instance
	api := createTestApi(t)
	defer api.Shutdown()

	// Create a simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with security headers using the API method
	secureHandler := api.WithSecurityHeaders(handler)
	require.NotNil(t, secureHandler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Execute request
	secureHandler.ServeHTTP(rec, req)

	// Verify security headers are applied
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
}

func TestSecurityHeadersNoCORSWithoutOrigin(t *testing.T) {
	// Create a simple handler for testing
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with security headers
	secureHandler := securityHeaders(handler)

	// Create test request without Origin header
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Execute request
	secureHandler.ServeHTTP(rec, req)

	// Verify CORS headers are NOT set when no Origin header
	headers := rec.Header()
	assert.Empty(t, headers.Get("Access-Control-Allow-Origin"))
	assert.Empty(t, headers.Get("Access-Control-Allow-Methods"))
	assert.Empty(t, headers.Get("Access-Control-Allow-Headers"))
	assert.Empty(t, headers.Get("Access-Control-Max-Age"))

	// But security headers should still be set
	assert.Equal(t, "nosniff", headers.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", headers.Get("X-Frame-Options"))
}
