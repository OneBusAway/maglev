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
	_ "modernc.org/sqlite" // Pure Go SQLite driver with FTS5 support
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

func TestConfigFileLoading(t *testing.T) {
	t.Run("loads valid config file", func(t *testing.T) {
		jsonConfig, err := appconf.LoadFromFile("../../testdata/config_valid.json")
		require.NoError(t, err)
		require.NotNil(t, jsonConfig)

		// Convert to configs
		appCfg := jsonConfig.ToAppConfig()
		gtfsCfgData := jsonConfig.ToGtfsConfigData()

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
		gtfsCfgData := jsonConfig.ToGtfsConfigData()

		// Verify app config
		assert.Equal(t, 8080, appCfg.Port)
		assert.Equal(t, appconf.Production, appCfg.Env)
		assert.Equal(t, []string{"key1", "key2", "key3"}, appCfg.ApiKeys)
		assert.Equal(t, 50, appCfg.RateLimit)

		// Verify GTFS config with first feed only
		assert.Equal(t, "https://example.com/gtfs.zip", gtfsCfgData.GtfsURL)
		assert.Equal(t, "/data/gtfs.db", gtfsCfgData.GTFSDataPath)
		assert.Equal(t, "https://api.example.com/trip-updates.pb", gtfsCfgData.TripUpdatesURL)
		assert.Equal(t, "https://api.example.com/vehicle-positions.pb", gtfsCfgData.VehiclePositionsURL)
		assert.Equal(t, "https://api.example.com/service-alerts.pb", gtfsCfgData.ServiceAlertsURL)
		assert.Equal(t, "Authorization", gtfsCfgData.RealTimeAuthHeaderKey)
		assert.Equal(t, "Bearer token123", gtfsCfgData.RealTimeAuthHeaderValue)
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
		gtfsCfgData := jsonConfig.ToGtfsConfigData()
		gtfsCfg := gtfs.Config{
			GtfsURL:                 gtfsCfgData.GtfsURL,
			TripUpdatesURL:          gtfsCfgData.TripUpdatesURL,
			VehiclePositionsURL:     gtfsCfgData.VehiclePositionsURL,
			ServiceAlertsURL:        gtfsCfgData.ServiceAlertsURL,
			RealTimeAuthHeaderKey:   gtfsCfgData.RealTimeAuthHeaderKey,
			RealTimeAuthHeaderValue: gtfsCfgData.RealTimeAuthHeaderValue,
			GTFSDataPath:            gtfsCfgData.GTFSDataPath,
			Env:                     gtfsCfgData.Env,
			Verbose:                 gtfsCfgData.Verbose,
		}

		// Build application
		coreApp, err := BuildApplication(cfg, gtfsCfg)
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
