package restapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
)

// Shared test database setup
var (
	testGtfsManager *gtfs.Manager
	testDbSetupOnce sync.Once
	testDbPath      = filepath.Join("../../testdata", "raba-test.db")
)

// TestMain handles setup and cleanup for all tests in this package
func TestMain(m *testing.M) {
	// Clean up any leftover test database from interrupted/failed previous runs
	_ = os.Remove(testDbPath)

	// Run all tests
	code := m.Run()

	// Clean up test database after all tests complete
	_ = os.Remove(testDbPath)

	os.Exit(code)
}

// createTestApiWithClock creates a new restAPI instance with a custom clock for deterministic testing.
// The GTFS database is created once and reused across all tests for performance.
func createTestApiWithClock(t testing.TB, c clock.Clock) *RestAPI {
	// Initialize the shared GTFS manager only once
	testDbSetupOnce.Do(func() {
		gtfsConfig := gtfs.Config{
			GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
			GTFSDataPath: testDbPath,
		}
		var err error
		testGtfsManager, err = gtfs.InitGTFSManager(gtfsConfig)
		if err != nil {
			t.Fatalf("Failed to initialize shared test GTFS manager: %v", err)
		}
	})

	gtfsConfig := gtfs.Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: testDbPath,
	}

	application := &app.Application{
		Config: appconf.Config{
			Env:           appconf.EnvFlagToEnvironment("test"),
			ApiKeys:       []string{"TEST", "test", "test-rate-limit", "test-headers", "test-refill", "test-error-format", "org.onebusaway.iphone"},
			RateLimit:     5, // Low rate limit for testing
			ExemptApiKeys: []string{"org.onebusaway.iphone"},
		},
		GtfsConfig:  gtfsConfig,
		GtfsManager: testGtfsManager,
		Clock:       c,
	}

	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return api
}

// createTestApi creates a new restAPI instance with a GTFS manager initialized for use in tests.
// Uses RealClock - for deterministic tests, use createTestApiWithClock with MockClock.
// Accepts testing.TB to support both *testing.T and *testing.B
func createTestApi(t testing.TB) *RestAPI {
	return createTestApiWithClock(t, clock.RealClock{})
}

// serveAndRetrieveEndpoint sets up a test server, makes a request to the specified endpoint, and returns the response
// and decoded model.
// Accepts testing.TB to support both *testing.T and *testing.B
func serveAndRetrieveEndpoint(t testing.TB, endpoint string) (*RestAPI, *http.Response, models.ResponseModel) {
	api := createTestApi(t)
	// Note: caller is responsible for calling api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)
	return api, resp, model
}

// serveApiAndRetrieveEndpoint performs the request against an existing API instance
// Accepts testing.TB to support both *testing.T and *testing.B
func serveApiAndRetrieveEndpoint(t testing.TB, api *RestAPI, endpoint string) (*http.Response, models.ResponseModel) {
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	resp, err := http.Get(server.URL + endpoint)
	require.NoError(t, err)
	defer logging.SafeCloseWithLogging(resp.Body,
		slog.Default().With(slog.String("component", "test")),
		"http_response_body")

	var response models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)

	return resp, response
}

func TestCompressionMiddleware(t *testing.T) {
	// Create a test handler that returns a large response
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write a large response that would benefit from compression
		w.Header().Set("Content-Type", "application/json")
		largeResponse := strings.Repeat(`{"test": "data"}`, 1000)
		_, _ = w.Write([]byte(largeResponse))
	})

	t.Run("compresses response when gzip accepted", func(t *testing.T) {
		// Create request with gzip acceptance
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		recorder := httptest.NewRecorder()

		// Apply compression middleware with default config
		handler := CompressionMiddleware(testHandler)
		handler.ServeHTTP(recorder, req)

		// Check response is compressed
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "gzip", recorder.Header().Get("Content-Encoding"))

		// Verify we can decompress the response
		reader, err := gzip.NewReader(bytes.NewReader(recorder.Body.Bytes()))
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		decompressed, err := io.ReadAll(reader)
		require.NoError(t, err)

		// Verify content
		expected := strings.Repeat(`{"test": "data"}`, 1000)
		assert.Equal(t, expected, string(decompressed))

		// Verify compression actually happened (compressed should be smaller)
		assert.Less(t, recorder.Body.Len(), len(expected))
	})

	t.Run("does not compress when gzip not accepted", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		// No Accept-Encoding header

		recorder := httptest.NewRecorder()

		handler := CompressionMiddleware(testHandler)
		handler.ServeHTTP(recorder, req)

		// Check response is not compressed
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Empty(t, recorder.Header().Get("Content-Encoding"))

		// Content should be uncompressed
		expected := strings.Repeat(`{"test": "data"}`, 1000)
		assert.Equal(t, expected, recorder.Body.String())
	})

	t.Run("handles empty responses", func(t *testing.T) {
		emptyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		recorder := httptest.NewRecorder()

		handler := CompressionMiddleware(emptyHandler)
		handler.ServeHTTP(recorder, req)

		assert.Equal(t, http.StatusNoContent, recorder.Code)
		assert.Empty(t, recorder.Body.String())
	})

	t.Run("preserves content-type header", func(t *testing.T) {
		jsonHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Use larger content to ensure compression happens
			largeJSON := strings.Repeat(`{"message": "test data"}`, 100)
			_, _ = w.Write([]byte(largeJSON))
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		recorder := httptest.NewRecorder()

		handler := CompressionMiddleware(jsonHandler)
		handler.ServeHTTP(recorder, req)

		assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
		assert.Equal(t, "gzip", recorder.Header().Get("Content-Encoding"))
	})
}

func TestCompressionMiddlewareIntegration(t *testing.T) {
	// Create a test API instance
	api := createTestApi(t)
	defer api.Shutdown()

	t.Run("API responses are compressed when requested", func(t *testing.T) {
		// Use the standard test setup approach
		mux := http.NewServeMux()
		api.SetRoutes(mux)
		server := httptest.NewServer(mux)
		defer server.Close()

		// Create request with gzip acceptance
		client := &http.Client{}
		req, err := http.NewRequest("GET", server.URL+"/api/where/agencies-with-coverage.json?key=TEST", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		// Check if the response was compressed - gzhttp may not compress small responses
		contentEncoding := resp.Header.Get("Content-Encoding")
		if contentEncoding == "gzip" {
			// Verify response can be decompressed
			reader, err := gzip.NewReader(resp.Body)
			require.NoError(t, err)
			defer func() { _ = reader.Close() }()

			decompressed, err := io.ReadAll(reader)
			require.NoError(t, err)

			// Should contain valid JSON
			assert.Contains(t, string(decompressed), `"code":200`)
		} else {
			// Response wasn't compressed (probably too small), verify it's valid JSON
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Contains(t, string(body), `"code":200`)
		}
	})
}

func TestCompressionConfig(t *testing.T) {
	t.Run("default config has sensible values", func(t *testing.T) {
		config := DefaultCompressionConfig()
		assert.Equal(t, 1024, config.MinSize)
		assert.Equal(t, 6, config.Level)
	})

	t.Run("custom config is applied", func(t *testing.T) {
		config := CompressionConfig{
			MinSize: 2048,
			Level:   9,
		}

		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Write response larger than MinSize to trigger compression
			largeResponse := strings.Repeat(`{"test": "data"}`, 500)
			_, _ = w.Write([]byte(largeResponse))
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")

		recorder := httptest.NewRecorder()

		middleware := NewCompressionMiddleware(config)
		handler := middleware(testHandler)
		handler.ServeHTTP(recorder, req)

		// Should still work with custom config
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	})
}
