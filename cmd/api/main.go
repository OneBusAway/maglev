package main

import (
	"context"
	"flag"
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

func main() {
	var cfg appconf.Config
	var gtfsCfg gtfs.Config
	var apiKeysFlag string
	var envFlag string

	flag.IntVar(&cfg.Port, "port", 4000, "API server port")
	flag.StringVar(&envFlag, "env", "development", "Environment (development|test|production)")
	flag.StringVar(&apiKeysFlag, "api-keys", "test", "Comma Separated API Keys (test, etc)")
	flag.IntVar(&cfg.RateLimit, "rate-limit", 100, "Requests per second per API key for rate limiting")
	flag.StringVar(&gtfsCfg.GtfsURL, "gtfs-url", "https://www.soundtransit.org/GTFS-rail/40_gtfs.zip", "URL for a static GTFS zip file")
	flag.StringVar(&gtfsCfg.TripUpdatesURL, "trip-updates-url", "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/trip-updates-for-agency/40.pb?key=org.onebusaway.iphone", "URL for a GTFS-RT trip updates feed")
	flag.StringVar(&gtfsCfg.VehiclePositionsURL, "vehicle-positions-url", "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/vehicle-positions-for-agency/40.pb?key=org.onebusaway.iphone", "URL for a GTFS-RT vehicle positions feed")
	flag.StringVar(&gtfsCfg.RealTimeAuthHeaderKey, "realtime-auth-header-name", "", "Optional header name for GTFS-RT auth")
	flag.StringVar(&gtfsCfg.RealTimeAuthHeaderValue, "realtime-auth-header-value", "", "Optional header value for GTFS-RT auth")
	flag.StringVar(&gtfsCfg.ServiceAlertsURL, "service-alerts-url", "", "URL for a GTFS-RT service alerts feed")
	flag.StringVar(&gtfsCfg.GTFSDataPath, "data-path", "./gtfs.db", "Path to the SQLite database containing GTFS data")
	flag.Parse()

	gtfsCfg.Verbose = true
	cfg.Verbose = true

	if apiKeysFlag != "" {
		cfg.ApiKeys = strings.Split(apiKeysFlag, ",")
		for i := range cfg.ApiKeys {
			cfg.ApiKeys[i] = strings.TrimSpace(cfg.ApiKeys[i])
		}
	}

	cfg.Env = appconf.EnvFlagToEnvironment(envFlag)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	gtfsManager, err := gtfs.InitGTFSManager(gtfsCfg)
	if err != nil {
		logger.Error("failed to initialize GTFS manager", "error", err)
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
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	logger.Info("starting server", "addr", srv.Addr, "env", cfg.Env)

	// Set up signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown server
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
	}

	// Shutdown GTFS manager
	if gtfsManager != nil {
		gtfsManager.Shutdown()
	}

	logger.Info("server exited")
}
