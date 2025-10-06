package main

import (
	"context"
	"fmt"
	"log/slog"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/restapi"
	"maglev.onebusaway.org/internal/webui"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
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

	var directionCalculator *gtfs.DirectionCalculator
	if gtfsManager != nil {
		directionCalculator = gtfs.NewDirectionCalculator(gtfsManager.GtfsDB.Queries)
	}

	coreApp := &app.Application{
		Config:              cfg,
		GtfsConfig:          gtfsCfg,
		Logger:              logger,
		GtfsManager:         gtfsManager,
		DirectionCalculator: directionCalculator,
	}

	return coreApp, nil
}

// CreateServer creates and configures the HTTP server with routes and middleware.
// Sets up both REST API routes and WebUI routes, applies security headers, and adds request logging.
func CreateServer(coreApp *app.Application, cfg appconf.Config) *http.Server {
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

	return srv
}

// Run manages the server lifecycle with graceful shutdown.
// Starts the server in a goroutine, waits for shutdown signals (SIGINT, SIGTERM),
// and performs graceful shutdown with a 30-second timeout.
// Returns an error if the server fails to start or shutdown fails.
func Run(srv *http.Server, gtfsManager *gtfs.Manager, logger *slog.Logger) error {
	logger.Info("starting server", "addr", srv.Addr)

	// Set up signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Channel to capture server errors
	serverErrors := make(chan error, 1)

	// Start server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrors <- err
		}
	}()

	// Wait for either shutdown signal or server error
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

	// Shutdown GTFS manager
	if gtfsManager != nil {
		gtfsManager.Shutdown()
	}

	logger.Info("server exited")
	return nil
}
