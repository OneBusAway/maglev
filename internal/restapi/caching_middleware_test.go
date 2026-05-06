package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCacheControlHeaders(t *testing.T) {
	api := createTestApi(t)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	tests := []struct {
		name           string
		endpoint       string
		expectedHeader string
	}{
		{
			name:           "Static Data (Long Cache)",
			endpoint:       "/api/where/agencies-with-coverage.json?key=TEST",
			expectedHeader: "public, max-age=300", // 5 minutes
		},
		{
			name:           "Real-time Data (Short Cache)",
			endpoint:       "/api/where/current-time.json?key=TEST",
			expectedHeader: "public, max-age=30", // 30 seconds
		},
		{
			name:           "User Reports (No Cache)",
			endpoint:       "/api/where/report-problem-with-stop/123.json?key=TEST",
			expectedHeader: "no-cache, no-store, must-revalidate", // 0 seconds
		},
		{
			name:           "Error Response (No Cache on 404)",
			endpoint:       "/api/where/stop/nonexistent_stop_id_123",
			expectedHeader: "no-cache, no-store, must-revalidate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.endpoint)
			assert.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			gotHeader := resp.Header.Get("Cache-Control")
			assert.Equal(t, tt.expectedHeader, gotHeader, "Cache-Control header mismatch for %s", tt.endpoint)
		})
	}
}

// TestCacheControlWriter_304PreservesCache proves the bug fix works
func TestCacheControlWriter_304PreservesCache(t *testing.T) {
	// Dummy handler that just returns 304 Not Modified
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})

	// Wrap in caching middleware set to 300 seconds
	wrapped := CacheControlMiddleware(300, handler)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	wrapped.ServeHTTP(rr, req)

	// It should preserve the cache header, NOT set it to no-cache
	assert.Equal(t, http.StatusNotModified, rr.Code)
	assert.Equal(t, "public, max-age=300", rr.Header().Get("Cache-Control"))
}

// TestETagMiddleware proves the conditional request logic works
func TestETagMiddleware(t *testing.T) {
	mockETag := `"test-hash-123"`
	getETag := func(_ *http.Request) string { return mockETag }

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response body"))
	})

	wrapped := ETagMiddleware(getETag)(handler)

	t.Run("No If-None-Match header", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		wrapped.ServeHTTP(rr, req)

		assert.True(t, handlerCalled, "Handler should be called on normal request")
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, mockETag, rr.Header().Get("ETag"))
	})

	t.Run("If-None-Match header matches", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-None-Match", mockETag)
		rr := httptest.NewRecorder()

		wrapped.ServeHTTP(rr, req)

		// Handler should NOT be called (short-circuited)
		assert.False(t, handlerCalled, "Handler should be bypassed on match")
		assert.Equal(t, http.StatusNotModified, rr.Code)
		assert.Empty(t, rr.Body.String())
		// RFC 7232 Compliance: 304 response MUST include the ETag header
		assert.Equal(t, mockETag, rr.Header().Get("ETag"))
	})

	t.Run("If-None-Match wildcard matches", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-None-Match", "*")
		rr := httptest.NewRecorder()

		wrapped.ServeHTTP(rr, req)

		// Handler should NOT be called (short-circuited)
		assert.False(t, handlerCalled, "Handler should be bypassed on wildcard match")
		assert.Equal(t, http.StatusNotModified, rr.Code)
		assert.Empty(t, rr.Body.String())
		// RFC 7232 Compliance: 304 response MUST include the ETag header
		assert.Equal(t, mockETag, rr.Header().Get("ETag"))
	})

	t.Run("If-None-Match header mismatch", func(t *testing.T) {
		handlerCalled = false
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-None-Match", `"wrong-hash"`)
		rr := httptest.NewRecorder()

		wrapped.ServeHTTP(rr, req)

		assert.True(t, handlerCalled)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, mockETag, rr.Header().Get("ETag"))
	})

	t.Run("Empty ETag from system gracefully falls back", func(t *testing.T) {
		handlerCalled = false
		emptyETagWrapped := ETagMiddleware(func(_ *http.Request) string { return "" })(handler)

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		emptyETagWrapped.ServeHTTP(rr, req)

		assert.True(t, handlerCalled)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Empty(t, rr.Header().Get("ETag"))
	})
}
