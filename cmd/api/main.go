package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
)

func main() {
	var cfg appconf.Config
	var gtfsCfg gtfs.Config
	var apiKeysFlag string
	var envFlag string
	var configFile string
	var dumpConfig bool

	// Parse command-line flags
	flag.StringVar(&configFile, "f", "", "Path to JSON configuration file (mutually exclusive with other flags)")
	flag.BoolVar(&dumpConfig, "dump-config", false, "Dump current configuration as JSON and exit")
	flag.IntVar(&cfg.Port, "port", 4000, "API server port")
	flag.StringVar(&envFlag, "env", "development", "Environment (development|test|production)")
	flag.StringVar(&apiKeysFlag, "api-keys", "test", "Comma Separated API Keys (test, etc)")
	flag.IntVar(&cfg.RateLimit, "rate-limit", 100, "Requests per second per API key for rate limiting")
	flag.StringVar(&gtfsCfg.GtfsURL, "gtfs-url", "https://www.soundtransit.org/GTFS-rail/40_gtfs.zip", "URL for a static GTFS zip file")
	flag.StringVar(&gtfsCfg.StaticAuthHeaderKey, "gtfs-static-auth-header-name", "", "Optional header name for static GTFS feed auth")
	flag.StringVar(&gtfsCfg.StaticAuthHeaderValue, "gtfs-static-auth-header-value", "", "Optional header value for static GTFS feed auth")
	flag.StringVar(&gtfsCfg.TripUpdatesURL, "trip-updates-url", "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/trip-updates-for-agency/40.pb?key=org.onebusaway.iphone", "URL for a GTFS-RT trip updates feed")
	flag.StringVar(&gtfsCfg.VehiclePositionsURL, "vehicle-positions-url", "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/vehicle-positions-for-agency/40.pb?key=org.onebusaway.iphone", "URL for a GTFS-RT vehicle positions feed")
	flag.StringVar(&gtfsCfg.RealTimeAuthHeaderKey, "realtime-auth-header-name", "", "Optional header name for GTFS-RT auth")
	flag.StringVar(&gtfsCfg.RealTimeAuthHeaderValue, "realtime-auth-header-value", "", "Optional header value for GTFS-RT auth")
	flag.StringVar(&gtfsCfg.ServiceAlertsURL, "service-alerts-url", "", "URL for a GTFS-RT service alerts feed")
	flag.StringVar(&gtfsCfg.GTFSDataPath, "data-path", "./gtfs.db", "Path to the SQLite database containing GTFS data")
	flag.Parse()

	// Enforce mutual exclusivity between -f and other flags (except --dump-config)
	if configFile != "" && flag.NFlag() > 1 {
		// Allow -f with --dump-config as a special case
		if flag.NFlag() != 2 || !dumpConfig {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			logger.Error("the -f flag is mutually exclusive with other configuration flags (except --dump-config)")
			flag.Usage()
			os.Exit(1)
		}
	}

	// Check for config file
	if configFile != "" {
		// Load configuration from JSON file
		jsonConfig, err := appconf.LoadFromFile(configFile)
		if err != nil {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			logger.Error("failed to load config file", "error", err)
			os.Exit(1)
		}

		// Convert to app config
		cfg = jsonConfig.ToAppConfig()

		// Convert to GTFS config
		gtfsCfgData := jsonConfig.ToGtfsConfigData()
		gtfsCfg = gtfs.Config{
			GtfsURL:                 gtfsCfgData.GtfsURL,
			StaticAuthHeaderKey:     gtfsCfgData.StaticAuthHeaderKey,
			StaticAuthHeaderValue:   gtfsCfgData.StaticAuthHeaderValue,
			TripUpdatesURL:          gtfsCfgData.TripUpdatesURL,
			VehiclePositionsURL:     gtfsCfgData.VehiclePositionsURL,
			ServiceAlertsURL:        gtfsCfgData.ServiceAlertsURL,
			RealTimeAuthHeaderKey:   gtfsCfgData.RealTimeAuthHeaderKey,
			RealTimeAuthHeaderValue: gtfsCfgData.RealTimeAuthHeaderValue,
			GTFSDataPath:            gtfsCfgData.GTFSDataPath,
			Env:                     gtfsCfgData.Env,
			Verbose:                 gtfsCfgData.Verbose,
			EnableGTFSTidy:          gtfsCfgData.EnableGTFSTidy,
		}
	} else {
		// Use command-line flags for configuration
		// Set verbosity flags
		gtfsCfg.Verbose = true
		cfg.Verbose = true

		// Parse API keys
		cfg.ApiKeys = ParseAPIKeys(apiKeysFlag)

		// Convert environment flag to enum
		cfg.Env = appconf.EnvFlagToEnvironment(envFlag)

		// Set GTFS config environment
		gtfsCfg.Env = cfg.Env
	}

	// Handle dump-config flag
	if dumpConfig {
		dumpConfigJSON(cfg, gtfsCfg)
		os.Exit(0)
	}

	// Build application with dependencies
	coreApp, err := BuildApplication(cfg, gtfsCfg)
	if err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		logger.Error("failed to build application", "error", err)
		os.Exit(1)
	}

	// Create HTTP server
	srv, api := CreateServer(coreApp, cfg)

	// Run server with graceful shutdown
	if err := Run(context.Background(), srv, coreApp.GtfsManager, api, coreApp.Logger); err != nil {
		coreApp.Logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
