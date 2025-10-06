package main

import (
	"flag"
	"log/slog"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
	"os"
)

func main() {
	var cfg appconf.Config
	var gtfsCfg gtfs.Config
	var apiKeysFlag string
	var envFlag string

	// Parse command-line flags
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

	// Set verbosity flags
	gtfsCfg.Verbose = true
	cfg.Verbose = true

	// Parse API keys
	cfg.ApiKeys = ParseAPIKeys(apiKeysFlag)

	// Convert environment flag to enum
	cfg.Env = appconf.EnvFlagToEnvironment(envFlag)

	// Build application with dependencies
	coreApp, err := BuildApplication(cfg, gtfsCfg)
	if err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		logger.Error("failed to build application", "error", err)
		os.Exit(1)
	}

	// Create HTTP server
	srv := CreateServer(coreApp, cfg)

	// Run server with graceful shutdown
	if err := Run(srv, coreApp.GtfsManager, coreApp.Logger); err != nil {
		coreApp.Logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
