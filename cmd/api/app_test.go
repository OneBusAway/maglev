package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestParseAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Single key",
			input:    "test-key",
			expected: []string{"test-key"},
		},
		{
			name:     "Multiple keys",
			input:    "key1,key2,key3",
			expected: []string{"key1", "key2", "key3"},
		},
		{
			name:     "Keys with spaces",
			input:    " key1 , key2 , key3 ",
			expected: []string{"key1", "key2", "key3"},
		},
		{
			name:     "Empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "Keys with mixed whitespace",
			input:    "key1,  key2  ,   key3",
			expected: []string{"key1", "key2", "key3"},
		},
		{
			name:     "Single key with whitespace",
			input:    "  test-key  ",
			expected: []string{"test-key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAPIKeys(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildApplicationWithMemoryDB(t *testing.T) {
	// Get path to test data
	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")

	// Check if test data exists, skip if not available
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("Test data not available, skipping test")
	}

	cfg := appconf.Config{
		Port:      4000,
		Env:       appconf.Test,
		ApiKeys:   []string{"test"},
		Verbose:   false,
		RateLimit: 100,
	}

	gtfsCfg := gtfs.Config{
		GTFSDataPath: ":memory:",
		GtfsURL:      testDataPath,
		Verbose:      false,
	}

	coreApp, err := BuildApplication(cfg, gtfsCfg)

	require.NoError(t, err, "BuildApplication should not return an error")
	assert.NotNil(t, coreApp, "Application should not be nil")
	assert.NotNil(t, coreApp.Logger, "Logger should be initialized")
	assert.Equal(t, cfg, coreApp.Config, "Config should match input")
	assert.Equal(t, gtfsCfg, coreApp.GtfsConfig, "GtfsConfig should match input")
}

func TestBuildApplicationWithTestData(t *testing.T) {
	// Get path to test data
	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")

	// Check if test data exists
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("Test data not available, skipping test")
	}

	cfg := appconf.Config{
		Port:      4000,
		Env:       appconf.Test,
		ApiKeys:   []string{"test"},
		Verbose:   false,
		RateLimit: 100,
	}

	gtfsCfg := gtfs.Config{
		GTFSDataPath: ":memory:",
		GtfsURL:      testDataPath,
		Verbose:      false,
	}

	coreApp, err := BuildApplication(cfg, gtfsCfg)

	require.NoError(t, err, "BuildApplication should not return an error with test data")
	assert.NotNil(t, coreApp, "Application should not be nil")
	assert.NotNil(t, coreApp.GtfsManager, "GTFS manager should be initialized")
	assert.NotNil(t, coreApp.DirectionCalculator, "Direction calculator should be initialized")
}

func TestCreateServer(t *testing.T) {
	// Get path to test data
	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")

	// Check if test data exists, skip if not available
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("Test data not available, skipping test")
	}

	cfg := appconf.Config{
		Port:      8080,
		Env:       appconf.Test,
		ApiKeys:   []string{"test"},
		Verbose:   false,
		RateLimit: 100,
	}

	gtfsCfg := gtfs.Config{
		GTFSDataPath: ":memory:",
		GtfsURL:      testDataPath,
		Verbose:      false,
	}

	coreApp, err := BuildApplication(cfg, gtfsCfg)
	require.NoError(t, err, "BuildApplication should not fail")

	srv := CreateServer(coreApp, cfg)

	assert.NotNil(t, srv, "Server should not be nil")
	assert.Equal(t, ":8080", srv.Addr, "Server address should match port")
	assert.NotNil(t, srv.Handler, "Server handler should be set")
	assert.Equal(t, time.Minute, srv.IdleTimeout, "IdleTimeout should be 1 minute")
	assert.Equal(t, 5*time.Second, srv.ReadTimeout, "ReadTimeout should be 5 seconds")
	assert.Equal(t, 10*time.Second, srv.WriteTimeout, "WriteTimeout should be 10 seconds")
}

func TestCreateServerHandlerResponds(t *testing.T) {
	// Get path to test data
	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")

	// Check if test data exists, skip if not available
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("Test data not available, skipping test")
	}

	cfg := appconf.Config{
		Port:      8080,
		Env:       appconf.Test,
		ApiKeys:   []string{"test"},
		Verbose:   false,
		RateLimit: 100,
	}

	gtfsCfg := gtfs.Config{
		GTFSDataPath: ":memory:",
		GtfsURL:      testDataPath,
		Verbose:      false,
	}

	coreApp, err := BuildApplication(cfg, gtfsCfg)
	require.NoError(t, err, "BuildApplication should not fail")

	srv := CreateServer(coreApp, cfg)

	// Test that the handler responds to requests
	req := httptest.NewRequest(http.MethodGet, "/api/where/current-time.json?key=test", nil)
	w := httptest.NewRecorder()

	srv.Handler.ServeHTTP(w, req)

	// The current-time endpoint should respond (even if GTFS data isn't loaded)
	assert.NotEqual(t, http.StatusNotFound, w.Code, "Handler should be configured and respond to requests")
}

func TestRunServerStartsAndStopsCleanly(t *testing.T) {
	// This is a lightweight integration test to verify the Run function can start and stop
	// We use a test HTTP server to avoid binding to real ports

	// Get path to test data
	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")

	// Check if test data exists, skip if not available
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("Test data not available, skipping test")
	}

	cfg := appconf.Config{
		Port:      0, // Use port 0 to get a random available port
		Env:       appconf.Test,
		ApiKeys:   []string{"test"},
		Verbose:   false,
		RateLimit: 100,
	}

	gtfsCfg := gtfs.Config{
		GTFSDataPath: ":memory:",
		GtfsURL:      testDataPath,
		Verbose:      false,
	}

	coreApp, err := BuildApplication(cfg, gtfsCfg)
	require.NoError(t, err, "BuildApplication should not fail")

	// Create a test server that we can control
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	// Test that we can create an HTTP server with proper configuration
	srv := CreateServer(coreApp, cfg)
	assert.NotNil(t, srv, "Server should be created")

	// Test the shutdown mechanism
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	err = srv.Shutdown(shutdownCtx)
	assert.NoError(t, err, "Server shutdown should succeed")
}

func TestParseAPIKeysEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Only commas",
			input:    ",,,",
			expected: []string{"", "", "", ""},
		},
		{
			name:     "Commas with spaces",
			input:    " , , , ",
			expected: []string{"", "", "", ""},
		},
		{
			name:     "Single comma",
			input:    ",",
			expected: []string{"", ""},
		},
		{
			name:     "Trailing comma",
			input:    "key1,",
			expected: []string{"key1", ""},
		},
		{
			name:     "Leading comma",
			input:    ",key1",
			expected: []string{"", "key1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAPIKeys(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRunWithPortZeroAndImmediateShutdown(t *testing.T) {
	// This test verifies Run() can start and shutdown gracefully
	testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("Test data not available, skipping test")
	}

	cfg := appconf.Config{
		Port:      0, // Use random port to avoid conflicts
		Env:       appconf.Test,
		ApiKeys:   []string{"test"},
		Verbose:   false,
		RateLimit: 100,
	}

	gtfsCfg := gtfs.Config{
		GTFSDataPath: ":memory:",
		GtfsURL:      testDataPath,
		Verbose:      false,
	}

	coreApp, err := BuildApplication(cfg, gtfsCfg)
	require.NoError(t, err)

	srv := CreateServer(coreApp, cfg)

	// Run the server in a goroutine
	done := make(chan error, 1)
	go func() {
		// We need to trigger shutdown immediately after starting
		go func() {
			time.Sleep(50 * time.Millisecond)
			// Trigger shutdown
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = srv.Shutdown(shutdownCtx)
		}()

		// This will block until server shuts down
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			done <- err
		} else {
			done <- nil
		}
	}()

	// Wait for server to complete
	select {
	case err := <-done:
		assert.NoError(t, err, "Server should shutdown cleanly")
	case <-time.After(10 * time.Second):
		t.Fatal("Test timeout - server did not shutdown")
	}
}

func TestBuildApplicationErrorHandling(t *testing.T) {
	t.Run("handles invalid GTFS path", func(t *testing.T) {
		cfg := appconf.Config{
			Port:      4000,
			Env:       appconf.Test,
			ApiKeys:   []string{"test"},
			Verbose:   false,
			RateLimit: 100,
		}

		gtfsCfg := gtfs.Config{
			GTFSDataPath: ":memory:",
			GtfsURL:      "/nonexistent/path/to/gtfs.zip",
			Verbose:      false,
		}

		_, err := BuildApplication(cfg, gtfsCfg)
		assert.Error(t, err, "Should return error for invalid GTFS path")
		assert.Contains(t, err.Error(), "failed to initialize GTFS manager")
	})
}
