package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
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
	ctx := context.Background()

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

	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)

	require.NoError(t, err, "BuildApplication should not return an error")
	assert.NotNil(t, coreApp, "Application should not be nil")
	assert.NotNil(t, coreApp.Logger, "Logger should be initialized")
	assert.Equal(t, cfg, coreApp.Config, "Config should match input")

	// BuildApplication injects the metrics client into the config.
	// We sync the injected Metrics over to our local copy so the assertion passes.
	gtfsCfg.Metrics = coreApp.GtfsConfig.Metrics
	assert.Equal(t, gtfsCfg, coreApp.GtfsConfig, "GtfsConfig should match input")
}

func TestBuildApplicationWithTestData(t *testing.T) {
	ctx := context.Background()

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

	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)

	require.NoError(t, err, "BuildApplication should not return an error with test data")
	assert.NotNil(t, coreApp, "Application should not be nil")
	assert.NotNil(t, coreApp.GtfsManager, "GTFS manager should be initialized")
	assert.NotNil(t, coreApp.DirectionCalculator, "Direction calculator should be initialized")
}

func TestCreateServer(t *testing.T) {
	ctx := context.Background()

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

	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)
	require.NoError(t, err, "BuildApplication should not fail")

	srv, api := CreateServer(coreApp, cfg)
	defer api.Shutdown()

	assert.NotNil(t, srv, "Server should not be nil")
	assert.Equal(t, ":8080", srv.Addr, "Server address should match port")
	assert.NotNil(t, srv.Handler, "Server handler should be set")
	assert.Equal(t, time.Minute, srv.IdleTimeout, "IdleTimeout should be 1 minute")
	assert.Equal(t, 5*time.Second, srv.ReadTimeout, "ReadTimeout should be 5 seconds")
	assert.Equal(t, 10*time.Second, srv.WriteTimeout, "WriteTimeout should be 10 seconds")
}

func TestCreateServerHandlerResponds(t *testing.T) {
	ctx := context.Background()

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

	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)
	require.NoError(t, err, "BuildApplication should not fail")

	srv, api := CreateServer(coreApp, cfg)
	defer api.Shutdown()

	// Test that the handler responds to requests. note: i am intentionally not testing
	// the healthz endpoint here since I want to make sure that the main API handler is
	// configured and responding. We test healthz separately.
	req := httptest.NewRequest(http.MethodGet, "/api/where/current-time.json?key=test", nil)
	w := httptest.NewRecorder()

	srv.Handler.ServeHTTP(w, req)

	// The current-time endpoint should respond (even if GTFS data isn't loaded)
	assert.NotEqual(t, http.StatusNotFound, w.Code, "Handler should be configured and respond to requests")
}

func TestRunServerStartsAndStopsCleanly(t *testing.T) {
	ctx := context.Background()

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

	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)
	require.NoError(t, err, "BuildApplication should not fail")

	// Create a test server that we can control
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer testServer.Close()

	// Test that we can create an HTTP server with proper configuration
	srv, api := CreateServer(coreApp, cfg)
	defer api.Shutdown()
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
	ctx := context.Background()

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

	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)
	require.NoError(t, err)

	srv, api := CreateServer(coreApp, cfg)
	defer api.Shutdown()

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
	ctx := context.Background()

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

		_, err := BuildApplication(ctx, cfg, gtfsCfg)
		assert.Error(t, err, "Should return error for invalid GTFS path")
		assert.Contains(t, err.Error(), "failed to initialize GTFS manager")
	})
}

func TestConfigFileLoading(t *testing.T) {
	t.Run("loads valid config file", func(t *testing.T) {
		jsonConfig, err := appconf.LoadFromFile("../../testdata/config_valid.json")
		require.NoError(t, err)
		require.NotNil(t, jsonConfig)

		// Convert to configs
		appCfg := jsonConfig.ToAppConfig()
		gtfsCfgData, err := jsonConfig.ToGtfsConfigData()
		require.NoError(t, err)

		// Verify app config
		assert.Equal(t, 3000, appCfg.Port)
		assert.Equal(t, appconf.Development, appCfg.Env)
		assert.Equal(t, []string{"test"}, appCfg.ApiKeys)
		assert.Equal(t, 100, appCfg.RateLimit)
		assert.True(t, appCfg.Verbose)

		// Verify GTFS config
		assert.Equal(t, appconf.Development, gtfsCfgData.Env)
		assert.True(t, gtfsCfgData.Verbose)
	})

	t.Run("loads full config file with GTFS-RT feed", func(t *testing.T) {
		jsonConfig, err := appconf.LoadFromFile("../../testdata/config_full.json")
		require.NoError(t, err)
		require.NotNil(t, jsonConfig)

		// Convert to configs
		appCfg := jsonConfig.ToAppConfig()
		gtfsCfgData, err := jsonConfig.ToGtfsConfigData()
		require.NoError(t, err)

		// Verify app config
		assert.Equal(t, 8080, appCfg.Port)
		assert.Equal(t, appconf.Production, appCfg.Env)
		assert.Equal(t, []string{"key1", "key2", "key3"}, appCfg.ApiKeys)
		assert.Equal(t, 50, appCfg.RateLimit)

		// Verify GTFS config - feeds are now in RTFeeds
		require.NotEmpty(t, gtfsCfgData.RTFeeds)
		feed0 := gtfsCfgData.RTFeeds[0]
		assert.Equal(t, "https://api.example.com/trip-updates.pb", feed0.TripUpdatesURL)
		assert.Equal(t, "https://api.example.com/vehicle-positions.pb", feed0.VehiclePositionsURL)
		assert.Equal(t, "https://api.example.com/service-alerts.pb", feed0.ServiceAlertsURL)
		assert.Equal(t, "Bearer token123", feed0.Headers["Authorization"])
	})

	t.Run("fails on invalid config file", func(t *testing.T) {
		jsonConfig, err := appconf.LoadFromFile("../../testdata/config_invalid.json")
		assert.Error(t, err)
		assert.Nil(t, jsonConfig)
		assert.Contains(t, err.Error(), "invalid configuration")
	})

	t.Run("fails on malformed JSON", func(t *testing.T) {
		jsonConfig, err := appconf.LoadFromFile("../../testdata/config_malformed.json")
		assert.Error(t, err)
		assert.Nil(t, jsonConfig)
		assert.Contains(t, err.Error(), "failed to parse JSON config")
	})

	t.Run("fails on nonexistent file", func(t *testing.T) {
		jsonConfig, err := appconf.LoadFromFile("../../testdata/nonexistent.json")
		assert.Error(t, err)
		assert.Nil(t, jsonConfig)
		assert.Contains(t, err.Error(), "failed to stat config file")
	})
}

func TestBuildApplicationWithConfigFile(t *testing.T) {
	ctx := context.Background()

	t.Run("builds app from valid config file", func(t *testing.T) {
		// Skip if test data not available
		testDataPath := filepath.Join("..", "..", "testdata", "raba.zip")
		if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
			t.Skip("Test data not available, skipping test")
		}

		// Convert to absolute path to avoid path traversal validation issues
		absTestDataPath, err := filepath.Abs(testDataPath)
		require.NoError(t, err)
		absTestDataPath = filepath.ToSlash(absTestDataPath)

		// Create a test config file that uses the test data
		testConfigPath := filepath.Join("..", "..", "testdata", "config_test_build.json")
		testConfigContent := `{
  "port": 5000,
  "env": "test",
  "api-keys": ["test-key"],
  "rate-limit": 50,
  "gtfs-url": "` + absTestDataPath + `",
  "data-path": ":memory:"
}`
		err = os.WriteFile(testConfigPath, []byte(testConfigContent), 0644)
		require.NoError(t, err)
		defer func() {
			_ = os.Remove(testConfigPath)
		}()

		// Load config from file
		jsonConfig, err := appconf.LoadFromFile(testConfigPath)
		require.NoError(t, err)

		// Convert to app and GTFS configs
		cfg := jsonConfig.ToAppConfig()
		gtfsCfgData, err := jsonConfig.ToGtfsConfigData()
		require.NoError(t, err)
		gtfsCfg := gtfsConfigFromData(gtfsCfgData)

		// Build application
		coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)
		require.NoError(t, err)
		assert.NotNil(t, coreApp)
		assert.NotNil(t, coreApp.Logger)
		assert.NotNil(t, coreApp.GtfsManager)
		assert.Equal(t, 5000, coreApp.Config.Port)
		assert.Equal(t, appconf.Test, coreApp.Config.Env)
		assert.Equal(t, []string{"test-key"}, coreApp.Config.ApiKeys)
		assert.Equal(t, 50, coreApp.Config.RateLimit)
	})
}

func TestRun_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	coreApp := &app.Application{}

	srv := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}

	errCh := make(chan error, 1)

	go func() {
		errCh <- Run(ctx, srv, coreApp, nil, logger)
	}()

	// Small delay so ListenAndServe starts
	time.Sleep(120 * time.Millisecond)

	// Trigger graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, "Run returned error on graceful shutdown")
	case <-time.After(3 * time.Second):
		assert.Fail(t, "server did not shut down in time")
	}
}

func TestDumpConfigJSON_WithExampleFile(t *testing.T) {
	// Load configuration from JSON file
	jsonConfig, err := appconf.LoadFromFile("../../config.example.json")

	require.NoError(t, err, "failed to load config.example.json")

	// Convert to app config
	cfg := jsonConfig.ToAppConfig()

	// Convert to GTFS config
	gtfsCfgData, err := jsonConfig.ToGtfsConfigData()
	require.NoError(t, err)
	gtfsCfg := gtfsConfigFromData(gtfsCfgData)

	if len(gtfsCfg.RTFeeds) > 0 {
		if gtfsCfg.RTFeeds[0].Headers == nil {
			gtfsCfg.RTFeeds[0].Headers = make(map[string]string)
		}
		gtfsCfg.RTFeeds[0].Headers["X-API-Key"] = "my-secret-api-key"
	}

	// Make a pipe to capture stdout
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()
	r, w, err := os.Pipe()
	require.NoError(t, err, "Failed to create OS Pipe")
	os.Stdout = w

	dumpConfigJSON(cfg, gtfsCfg)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	output := buf.String()

	// Parse and validate json
	var parsed map[string]interface{}

	err = json.Unmarshal([]byte(output), &parsed)
	require.NoError(t, err, "Output is not a valid JSON")

	assert.Equal(t, float64(cfg.Port), parsed["port"])
	staticFeed, ok := parsed["gtfs-static-feed"].(map[string]interface{})
	require.True(t, ok, "gtfs-static-feed should be a map")
	assert.Equal(t, gtfsCfg.GtfsURL, staticFeed["url"])

	feeds, ok := parsed["gtfs-rt-feeds"].([]interface{})
	require.True(t, ok, "gtfs-rt-feeds should be an array of maps")
	assert.Equal(t, len(gtfsCfg.RTFeeds), len(feeds))

	rtFeed, ok := feeds[0].(map[string]interface{})
	require.True(t, ok, "feeds[0] should be a map")

	// Check that headers are redacted (:
	headersMap, ok := rtFeed["headers"].(map[string]interface{})
	require.True(t, ok, "headers should be a map")
	assert.NotEqual(t, "my-secret-api-key", headersMap["X-API-Key"])
	assert.Equal(t, "***REDACTED***", headersMap["X-API-Key"])

	// Check that API keys are redacted
	apiKeysStr, ok := parsed["api-keys"].(string)
	require.True(t, ok, "api-keys should be a string")
	assert.Contains(t, apiKeysStr, "REDACTED", "API keys should be redacted")

	exemptApiKeysStr, ok := parsed["exempt-api-keys"].(string)
	require.True(t, ok, "exempt-api-keys should be a string")
	assert.Contains(t, exemptApiKeysStr, "REDACTED", "Exempt API keys should be redacted")

	assert.NotEqual(t, "", rtFeed["trip-updates-url"])
	assert.Equal(t, gtfsCfg.GTFSDataPath, parsed["data-path"])
}
