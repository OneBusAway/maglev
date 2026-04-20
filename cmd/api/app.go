package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/metrics"
	"maglev.onebusaway.org/internal/restapi"
	"maglev.onebusaway.org/internal/webui"
)

func gtfsConfigFromData(gtfsCfgData appconf.GtfsConfigData) gtfs.Config {
	gtfsCfg := gtfs.Config{
		GtfsURL:               gtfsCfgData.GtfsURL,
		StaticAuthHeaderKey:   gtfsCfgData.StaticAuthHeaderKey,
		StaticAuthHeaderValue: gtfsCfgData.StaticAuthHeaderValue,
		GTFSDataPath:          gtfsCfgData.GTFSDataPath,
		Env:                   gtfsCfgData.Env,
		Verbose:               gtfsCfgData.Verbose,
		EnableGTFSTidy:        gtfsCfgData.EnableGTFSTidy,
	}

	for _, feedData := range gtfsCfgData.RTFeeds {
		gtfsCfg.RTFeeds = append(gtfsCfg.RTFeeds, gtfs.RTFeedConfig{
			ID:                  feedData.ID,
			AgencyIDs:           feedData.AgencyIDs,
			TripUpdatesURL:      feedData.TripUpdatesURL,
			VehiclePositionsURL: feedData.VehiclePositionsURL,
			ServiceAlertsURL:    feedData.ServiceAlertsURL,
			Headers:             feedData.Headers,
			RefreshInterval:     feedData.RefreshInterval,
			Enabled:             feedData.Enabled,
		})
	}

	return gtfsCfg
}

// ParseAPIKeys splits a comma-separated string of API keys and trims whitespace from each key.
// Returns an empty slice if the input is empty.
func ParseAPIKeys(apiKeysFlag string) []string {
	if apiKeysFlag == "" {
		return []string{}
	}

	keys := strings.Split(apiKeysFlag, ",")
	for i := range keys {
		keys[i] = strings.TrimSpace(keys[i])
	}
	return keys
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newLogHandler(format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(format, "json") {
		return slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.NewTextHandler(os.Stdout, opts)
}

// BuildApplication creates and initializes the Application with all dependencies.
// This includes creating the logger, initializing the GTFS manager, and creating the direction calculator.
// Returns an error if GTFS manager initialization fails.
func BuildApplication(ctx context.Context, cfg appconf.Config, gtfsCfg gtfs.Config) (*app.Application, error) {
	level := parseLogLevel(cfg.LogLevel)
	logger := slog.New(newLogHandler(cfg.LogFormat, level))

	appMetrics := metrics.NewWithLogger(logger)
	gtfsCfg.Metrics = appMetrics

	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GTFS manager: %w", err)
	}

	var directionCalculator *gtfs.AdvancedDirectionCalculator
	if gtfsManager != nil {
		directionCalculator = gtfs.NewAdvancedDirectionCalculator(gtfsManager.GtfsDB.Queries)
		// Register the calculator on the manager so ForceUpdate can refresh its
		// queries pointer (and evict the direction cache) after every DB hot-swap.
		gtfsManager.DirectionCalculator = directionCalculator
	}

	// Select clock implementation based on environment
	appClock := createClock(cfg.Env)

	coreApp := &app.Application{
		Config:              cfg,
		GtfsConfig:          gtfsCfg,
		Logger:              logger,
		GtfsManager:         gtfsManager,
		DirectionCalculator: directionCalculator,
		Clock:               appClock,
		Metrics:             appMetrics,
	}

	// Start DB stats collector using a provider so metrics follow DB hot-swap.
	if gtfsManager != nil {
		appMetrics.StartDBStatsCollector(func() *sql.DB {
			gtfsManager.RLock()
			defer gtfsManager.RUnlock()
			if gtfsManager.GtfsDB == nil {
				return nil
			}
			return gtfsManager.GtfsDB.DB
		}, 15*time.Second)
	}

	return coreApp, nil
}

// createClock returns the appropriate Clock implementation based on environment.
// - Production/Development: RealClock (uses actual system time)
// - Test: EnvironmentClock (reads from FAKETIME env var or file, fallback to system time)
func createClock(env appconf.Environment) clock.Clock {
	switch env {
	case appconf.Test:
		return clock.NewEnvironmentClock("FAKETIME", "/etc/faketimerc", time.Local)
	default:
		return clock.RealClock{}
	}
}

// CreateServer creates and configures the HTTP server with routes and middleware.
// Sets up both REST API routes and WebUI routes, applies security headers, and adds request logging.
func CreateServer(coreApp *app.Application, cfg appconf.Config) (*http.Server, *restapi.RestAPI) {
	api := restapi.NewRestAPI(coreApp)

	webUI := &webui.WebUI{
		Application: coreApp,
	}

	mux := http.NewServeMux()

	api.SetRoutes(mux)
	webUI.SetWebUIRoutes(mux)

	// Add metrics endpoint (no auth required) - uses custom registry with structured error logging
	mux.Handle("GET /metrics", promhttp.HandlerFor(coreApp.Metrics.Registry, promhttp.HandlerOpts{
		ErrorLog: slog.NewLogLogger(coreApp.Logger.Handler(), slog.LevelError),
	}))

	// Apply global compression around the entire mux
	compressedMux := restapi.CompressionMiddleware(mux)

	// Add freshness middleware
	freshnessHandler := api.FreshnessMiddleware(compressedMux)

	// Wrap with security middleware
	secureHandler := api.WithSecurityHeaders(freshnessHandler)

	// Add metrics middleware
	metricsHandler := restapi.MetricsHandler(coreApp.Metrics)(secureHandler)

	// Add request logging middleware (outermost)
	requestLogMiddleware := restapi.NewRequestLoggingMiddleware(coreApp.Logger)

	sizeLimitMiddleware := restapi.SizeLimitMiddleware(1 << 20) // 1 MB limit

	// Panic recovery outermost so all handler panics are caught
	handler := restapi.NewRecoveryMiddleware(coreApp.Logger, coreApp.Clock)(
		sizeLimitMiddleware(restapi.RequestIDMiddleware(requestLogMiddleware(metricsHandler))),
	)

	srv := &http.Server{
		Addr:           fmt.Sprintf(":%d", cfg.Port),
		Handler:        handler,
		IdleTimeout:    time.Minute,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB (explicit; matches Go's default)
		ErrorLog:       slog.NewLogLogger(coreApp.Logger.Handler(), slog.LevelError),
	}

	return srv, api
}

// Run manages the server lifecycle with graceful shutdown.
// Starts the server in a goroutine, waits for shutdown signals (SIGINT, SIGTERM) or context cancellation,
// and performs graceful shutdown with a 30-second timeout.
// Returns an error if the server fails to start or shutdown fails.
func Run(ctx context.Context, srv *http.Server, coreApp *app.Application, api *restapi.RestAPI, logger *slog.Logger) error {
	cfg := coreApp.Config
	tlsEnabled := cfg.TLSCertPath != "" && cfg.TLSKeyPath != ""
	logger.Info("starting server", "addr", srv.Addr, "tls", tlsEnabled)

	// Set up signal handling for graceful shutdown, merging with provided context
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Channel to capture server errors
	serverErrors := make(chan error, 1)

	// Start server in a goroutine
	go func() {
		var err error
		if tlsEnabled {
			err = srv.ListenAndServeTLS(cfg.TLSCertPath, cfg.TLSKeyPath)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			serverErrors <- err
		}
	}()

	// Wait for either shutdown signal/context cancellation or server error
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server failed to start: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down server...")
	}

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown server
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		return fmt.Errorf("server forced to shutdown: %w", err)
	}

	// Shutdown API rate limiter first (stops background goroutines for request handling)
	if api != nil {
		api.Shutdown()
	}

	// Shutdown metrics collector (blocks until goroutine exits)
	if coreApp.Metrics != nil {
		coreApp.Metrics.Shutdown()
	}

	// Then shutdown GTFS manager (stops data fetching - the lowest-level dependency)
	if coreApp.GtfsManager != nil {
		coreApp.GtfsManager.Shutdown()
	}

	logger.Info("server exited")
	return nil
}

// dumpConfigJSON converts current configuration to JSON and prints it to stdout
func dumpConfigJSON(cfg appconf.Config, gtfsCfg gtfs.Config) {
	// Convert environment enum to string
	envStr := "development"
	switch cfg.Env {
	case appconf.Development:
		envStr = "development"
	case appconf.Test:
		envStr = "test"
	case appconf.Production:
		envStr = "production"
	}

	// Build gtfs-static-feed object
	staticAuthValue := gtfsCfg.StaticAuthHeaderValue
	if staticAuthValue != "" {
		staticAuthValue = "***REDACTED***"
	}
	staticFeed := map[string]string{
		"url": gtfsCfg.GtfsURL,
	}
	if gtfsCfg.StaticAuthHeaderKey != "" {
		staticFeed["auth-header-name"] = gtfsCfg.StaticAuthHeaderKey
		staticFeed["auth-header-value"] = staticAuthValue
	}

	// Build JSON config structure
	jsonConfig := map[string]any{
		"port":             cfg.Port,
		"env":              envStr,
		"api-keys":         fmt.Sprintf("***REDACTED*** (%d keys)", len(cfg.ApiKeys)),
		"exempt-api-keys":  fmt.Sprintf("***REDACTED*** (%d keys)", len(cfg.ExemptApiKeys)),
		"rate-limit":       cfg.RateLimit,
		"gtfs-static-feed": staticFeed,
		"data-path":        gtfsCfg.GTFSDataPath,
	}

	var feeds []map[string]any
	for _, feedCfg := range gtfsCfg.RTFeeds {
		redactedHeaders := make(map[string]string)
		for k := range feedCfg.Headers {
			redactedHeaders[k] = "***REDACTED***"
		}

		feed := map[string]any{
			"id":                    feedCfg.ID,
			"trip-updates-url":      feedCfg.TripUpdatesURL,
			"vehicle-positions-url": feedCfg.VehiclePositionsURL,
			"service-alerts-url":    feedCfg.ServiceAlertsURL,
			"refresh-interval":      feedCfg.RefreshInterval,
			"enabled":               feedCfg.Enabled,
		}
		if len(feedCfg.AgencyIDs) > 0 {
			feed["agency-ids"] = feedCfg.AgencyIDs
		}
		if len(redactedHeaders) > 0 {
			feed["headers"] = redactedHeaders
		}
		feeds = append(feeds, feed)
	}
	jsonConfig["gtfs-rt-feeds"] = feeds

	// Marshal to JSON with indentation
	output, err := json.MarshalIndent(jsonConfig, "", "  ")
	if err != nil {
		logger := slog.Default().With(slog.String("component", "app"))
		logging.LogError(logger, "Error marshaling config to JSON", err)
		os.Exit(1)
	}

	fmt.Println(string(output))
}
