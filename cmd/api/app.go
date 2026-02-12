package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/restapi"
	"maglev.onebusaway.org/internal/webui"
)

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

// BuildApplication creates and initializes the Application with all dependencies.
// This includes creating the logger, initializing the GTFS manager, and creating the direction calculator.
// Returns an error if GTFS manager initialization fails.
func BuildApplication(cfg appconf.Config, gtfsCfg gtfs.Config) (*app.Application, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	gtfsManager, err := gtfs.InitGTFSManager(gtfsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GTFS manager: %w", err)
	}

	var directionCalculator *gtfs.AdvancedDirectionCalculator
	if gtfsManager != nil {
		directionCalculator = gtfs.NewAdvancedDirectionCalculator(gtfsManager.GtfsDB.Queries)
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

	// Wrap with security middleware
	secureHandler := api.WithSecurityHeaders(mux)

	// Add request logging middleware (outermost)
	requestLogger := logging.NewStructuredLogger(os.Stdout, slog.LevelInfo)
	requestLogMiddleware := restapi.NewRequestLoggingMiddleware(requestLogger)
	handler := requestLogMiddleware(secureHandler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      handler,
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		ErrorLog:     slog.NewLogLogger(coreApp.Logger.Handler(), slog.LevelError),
	}

	return srv, api
}

// Run manages the server lifecycle with graceful shutdown.
// Starts the server in a goroutine, waits for shutdown signals (SIGINT, SIGTERM) or context cancellation,
// and performs graceful shutdown with a 30-second timeout.
// Returns an error if the server fails to start or shutdown fails.
func Run(ctx context.Context, srv *http.Server, gtfsManager *gtfs.Manager, api *restapi.RestAPI, logger *slog.Logger) error {
	logger.Info("starting server", "addr", srv.Addr)

	// Set up signal handling for graceful shutdown, merging with provided context
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Channel to capture server errors
	serverErrors := make(chan error, 1)

	// Start server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

	// Then shutdown GTFS manager (stops data fetching - the lowest-level dependency)
	if gtfsManager != nil {
		gtfsManager.Shutdown()
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
	jsonConfig := map[string]interface{}{
		"port":             cfg.Port,
		"env":              envStr,
		"api-keys":         cfg.ApiKeys,
		"rate-limit":       cfg.RateLimit,
		"gtfs-static-feed": staticFeed,
		"data-path":        gtfsCfg.GTFSDataPath,
	}

	// Add GTFS-RT feed if configured
	feeds := []map[string]string{}
	if gtfsCfg.TripUpdatesURL != "" || gtfsCfg.VehiclePositionsURL != "" {
		// Mask sensitive auth header value
		authHeaderValue := gtfsCfg.RealTimeAuthHeaderValue
		if authHeaderValue != "" {
			authHeaderValue = "***REDACTED***"
		}

		feed := map[string]string{
			"trip-updates-url":           gtfsCfg.TripUpdatesURL,
			"vehicle-positions-url":      gtfsCfg.VehiclePositionsURL,
			"service-alerts-url":         gtfsCfg.ServiceAlertsURL,
			"realtime-auth-header-name":  gtfsCfg.RealTimeAuthHeaderKey,
			"realtime-auth-header-value": authHeaderValue,
		}
		feeds = append(feeds, feed)
	}
	jsonConfig["gtfs-rt-feeds"] = feeds

	// Marshal to JSON with indentation
	output, err := json.MarshalIndent(jsonConfig, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling config to JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(output))
}
