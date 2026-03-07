package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // Auto-registers pprof handlers to http.DefaultServeMux
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
)

func main() {
	// From fix/496-gtfs-startup-retry: Graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start isolated pprof server on localhost only
	if os.Getenv("MAGLEV_ENABLE_PPROF") == "1" {
		go func() {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With(slog.String("component", "pprof"))
			logger.Warn("STARTING PPROF DEBUG SERVER ON localhost:6060 (NOT PUBLIC)")
			// Listens ONLY on loopback interface using DefaultServeMux
			if err := http.ListenAndServe("127.0.0.1:6060", nil); err != nil {
				logger.Error("pprof debug server failed", "error", err)
			}
		}()
	}

	// From main: Mutex profiling configuration
	if os.Getenv("MAGLEV_PROFILE_MUTEX") == "1" {
		runtime.SetMutexProfileFraction(1)
		runtime.SetBlockProfileRate(1)

		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		logger.Warn("MUTEX AND BLOCK PROFILING ENABLED (Performance will be impacted)")
	}

	var cfg appconf.Config
	var gtfsCfg gtfs.Config
	var apiKeysFlag string
	var exemptApiKeysFlag string
	var envFlag string
	var configFile string
	var dumpConfig bool

	// CLI-only realtime feed fields (assembled into RTFeeds slice below)
	var cliFeedTripUpdatesURL string
	var cliFeedVehiclePositionsURL string
	var cliFeedServiceAlertsURL string
	var cliFeedAuthHeaderName string
	var cliFeedAuthHeaderValue string

	// Parse command-line flags
	flag.StringVar(&configFile, "f", "", "Path to JSON configuration file (mutually exclusive with other flags)")
	flag.BoolVar(&dumpConfig, "dump-config", false, "Dump current configuration as JSON and exit")
	flag.IntVar(&cfg.Port, "port", 4000, "API server port")
	flag.StringVar(&envFlag, "env", "development", "Environment (development|test|production)")
	flag.StringVar(&apiKeysFlag, "api-keys", "test", "Comma Separated API Keys (test, etc)")
	flag.StringVar(&exemptApiKeysFlag, "exempt-api-keys", "org.onebusaway.iphone", "Comma separated list of API keys exempt from rate limiting")
	flag.IntVar(&cfg.RateLimit, "rate-limit", 100, "Requests per second per API key for rate limiting")
	flag.StringVar(&gtfsCfg.GtfsURL, "gtfs-url", "https://www.soundtransit.org/GTFS-rail/40_gtfs.zip", "URL for a static GTFS zip file")
	flag.StringVar(&gtfsCfg.StaticAuthHeaderKey, "gtfs-static-auth-header-name", "", "Optional header name for static GTFS feed auth")
	flag.StringVar(&gtfsCfg.StaticAuthHeaderValue, "gtfs-static-auth-header-value", "", "Optional header value for static GTFS feed auth")
	flag.StringVar(&cliFeedTripUpdatesURL, "trip-updates-url", "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/trip-updates-for-agency/40.pb?key=org.onebusaway.iphone", "URL for a GTFS-RT trip updates feed")
	flag.StringVar(&cliFeedVehiclePositionsURL, "vehicle-positions-url", "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/vehicle-positions-for-agency/40.pb?key=org.onebusaway.iphone", "URL for a GTFS-RT vehicle positions feed")
	flag.StringVar(&cliFeedAuthHeaderName, "realtime-auth-header-name", "", "Optional header name for GTFS-RT auth")
	flag.StringVar(&cliFeedAuthHeaderValue, "realtime-auth-header-value", "", "Optional header value for GTFS-RT auth")
	flag.StringVar(&cliFeedServiceAlertsURL, "service-alerts-url", "", "URL for a GTFS-RT service alerts feed")
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
		gtfsCfgData, err := jsonConfig.ToGtfsConfigData()
		if err != nil {
			slog.Error("failed to convert config", "error", err)
			os.Exit(1)
		}
		gtfsCfg = gtfsConfigFromData(gtfsCfgData)

	} else {
		// Use command-line flags for configuration

		// Pack the CLI flags into a temporary JSONConfig struct
		// This allows us to run the exact same robust validation logic as the JSON path!
		cliConfig := appconf.JSONConfig{
			Port:          cfg.Port,
			Env:           envFlag,
			ApiKeys:       ParseAPIKeys(apiKeysFlag),
			ExemptApiKeys: ParseAPIKeys(exemptApiKeysFlag),
			RateLimit:     cfg.RateLimit,
			GtfsStaticFeed: appconf.GtfsStaticFeed{
				URL:             gtfsCfg.GtfsURL,
				AuthHeaderName:  gtfsCfg.StaticAuthHeaderKey,
				AuthHeaderValue: gtfsCfg.StaticAuthHeaderValue,
			},
			GtfsRtFeeds: []appconf.GtfsRtFeed{
				{
					ID:                      "feed-0",
					TripUpdatesURL:          cliFeedTripUpdatesURL,
					VehiclePositionsURL:     cliFeedVehiclePositionsURL,
					ServiceAlertsURL:        cliFeedServiceAlertsURL,
					RealTimeAuthHeaderName:  cliFeedAuthHeaderName,
					RealTimeAuthHeaderValue: cliFeedAuthHeaderValue,
					RefreshInterval:         30,
				},
			},
			DataPath: gtfsCfg.GTFSDataPath,
		}

		// Run the shared validation logic
		if err := cliConfig.Validate(); err != nil {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			logger.Error("invalid command-line configuration", "error", err)
			os.Exit(1)
		}

		// Convert to internal app configs (DRY!)
		cfg = cliConfig.ToAppConfig()

		gtfsCfgData, err := cliConfig.ToGtfsConfigData()
		if err != nil {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			logger.Error("failed to convert command-line config", "error", err)
			os.Exit(1)
		}
		gtfsCfg = gtfsConfigFromData(gtfsCfgData)

		// Set verbosity flags (CLI specific)
		gtfsCfg.Verbose = true
		cfg.Verbose = true
	}

	// Handle dump-config flag
	if dumpConfig {
		dumpConfigJSON(cfg, gtfsCfg)
		os.Exit(0)
	}

	// Build application with dependencies
	coreApp, err := BuildApplication(ctx, cfg, gtfsCfg)
	if err != nil {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		logger.Error("failed to build application", "error", err)
		os.Exit(1)
	}

	// Create HTTP server
	srv, api := CreateServer(coreApp, cfg)

	// Run server with graceful shutdown
	if err := Run(ctx, srv, coreApp, api, coreApp.Logger); err != nil {
		coreApp.Logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
