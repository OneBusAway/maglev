package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestRealtimeEndpointsAreNotCachedAsStatic guards the endpoints that put real-time
// service alerts in references.situations. The ETag is the static feed's file hash, so
// serving one alongside alert data hands clients a 304 for as long as the feed is
// unchanged, however often the alerts move.
func TestRealtimeEndpointsAreNotCachedAsStatic(t *testing.T) {
	api := createTestApi(t)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	endpoints := []struct {
		name     string
		endpoint string
	}{
		{"search stop", "/api/where/search/stop.json?input=Buenaventura&key=TEST"},
		{"search route", "/api/where/search/route.json?input=Route&key=TEST"},
		{"route", "/api/where/route/25_151.json?key=TEST"},
	}

	for _, tt := range endpoints {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.endpoint)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Empty(t, resp.Header.Get("ETag"),
				"%s carries real-time alerts and must not be validated against the static feed hash", tt.endpoint)
			assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"),
				"%s carries real-time alerts and must not be cached as static", tt.endpoint)
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

// TestETagMiddleware_StaticFeedHash preserves conditional requests for static feed hashes.
func TestETagMiddleware_StaticFeedHash(t *testing.T) {
	api := createTestApi(t)
	path := "/api/where/agencies-with-coverage.json?key=" + vehiclePositionsKey
	first, _ := getRaw(t, api, path, nil)
	require.Equal(t, http.StatusOK, first.StatusCode)
	etag := first.Header.Get("ETag")
	require.NotEmpty(t, etag)

	for _, tt := range []struct {
		name         string
		headerValues []string
		wantStatus   int
	}{
		{"exact feed hash", []string{etag}, http.StatusNotModified},
		{"feed hash in a list", []string{`"other", ` + etag}, http.StatusNotModified},
		{"feed hash in second header line", []string{`"other"`, etag}, http.StatusNotModified},
		{"different feed hash", []string{`"other"`}, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := getRaw(t, api, path, http.Header{"If-None-Match": tt.headerValues})
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantStatus == http.StatusNotModified {
				assert.Empty(t, body)
			} else {
				assert.NotEmpty(t, body)
			}
			assert.Equal(t, etag, resp.Header.Get("ETag"))
			assert.Equal(t, "public, max-age=300", resp.Header.Get("Cache-Control"))
		})
	}
}

func TestIfNoneMatchHits(t *testing.T) {
	const etag = `W/"abc"`
	tests := []struct {
		name        string
		ifNoneMatch string
		want        bool
	}{
		{"absent", "", false},
		{"wildcard", "*", true},
		{"exact", `W/"abc"`, true},
		{"in a list", `"x", W/"abc"`, true},
		{"different", `W/"other"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.ifNoneMatch != "" {
				req.Header.Set("If-None-Match", tt.ifNoneMatch)
			}
			assert.Equal(t, tt.want, ifNoneMatchHits(req, etag))
		})
	}
}

// TestIfNoneMatchHits_ConditionalValidators checks weak comparison and all header lines.
func TestIfNoneMatchHits_ConditionalValidators(t *testing.T) {
	tests := []struct {
		name         string
		etag         string
		headerValues []string
		want         bool
	}{
		{
			name:         "weak request matches strong response",
			etag:         `"abc"`,
			headerValues: []string{`W/"abc"`},
			want:         true,
		},
		{
			name:         "strong request matches weak response",
			etag:         `W/"abc"`,
			headerValues: []string{`"abc"`},
			want:         true,
		},
		{
			name:         "equivalent validator in a list",
			etag:         `W/"abc"`,
			headerValues: []string{`"other", "abc"`},
			want:         true,
		},
		{
			name:         "matching validator in second header line",
			etag:         `W/"abc"`,
			headerValues: []string{`"other"`, `W/"abc"`},
			want:         true,
		},
		{
			name:         "different validators across header lines",
			etag:         `W/"abc"`,
			headerValues: []string{`"other"`, `W/"different"`},
			want:         false,
		},
		{
			name:         "opaque tag comparison remains case sensitive",
			etag:         `W/"abc"`,
			headerValues: []string{`"ABC"`},
			want:         false,
		},
		{
			name:         "comma inside a quoted tag is preserved",
			etag:         `W/"abc,def"`,
			headerValues: []string{`"other", W/"abc,def"`},
			want:         true,
		},
		{
			name:         "empty opaque tag is valid",
			etag:         `""`,
			headerValues: []string{`W/""`},
			want:         true,
		},
		{
			name:         "whitespace and empty list elements are ignored",
			etag:         `W/"abc"`,
			headerValues: []string{" ,\t \"other\",, W/\"abc\", "},
			want:         true,
		},
		{
			name:         "unquoted tag does not match",
			etag:         `"abc"`,
			headerValues: []string{`abc`},
			want:         false,
		},
		{
			name:         "unterminated tag does not match",
			etag:         `"abc"`,
			headerValues: []string{`W/"abc`},
			want:         false,
		},
		{
			name:         "space inside a tag is invalid",
			etag:         `"abc"`,
			headerValues: []string{`"bad tag", "abc"`},
			want:         false,
		},
		{
			name:         "DEL inside a tag is invalid",
			etag:         `"abc"`,
			headerValues: []string{"\"bad\x7ftag\", \"abc\""},
			want:         false,
		},
		{
			name:         "missing list separator is invalid",
			etag:         `"abc"`,
			headerValues: []string{`"other" W/"abc"`},
			want:         false,
		},
		{
			name:         "backslash inside a tag is literal",
			etag:         `W/"abc\"`,
			headerValues: []string{`"abc\"`},
			want:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range tt.headerValues {
				req.Header.Add("If-None-Match", value)
			}
			assert.Equal(t, tt.want, ifNoneMatchHits(req, tt.etag))
		})
	}
}
