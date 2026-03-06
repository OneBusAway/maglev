package appconf

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// GtfsStaticFeed represents the static GTFS feed configuration
type GtfsStaticFeed struct {
	URL             string `json:"url"`
	AuthHeaderName  string `json:"auth-header-name"`
	AuthHeaderValue string `json:"auth-header-value"`
	EnableGTFSTidy  bool   `json:"enable-gtfs-tidy"`
}

// GtfsRtFeed represents a single GTFS-RT feed configuration
type GtfsRtFeed struct {
	ID                      string            `json:"id"`
	AgencyIDs               []string          `json:"agency-ids"` // When set, only realtime data for these agencies is included
	TripUpdatesURL          string            `json:"trip-updates-url"`
	VehiclePositionsURL     string            `json:"vehicle-positions-url"`
	ServiceAlertsURL        string            `json:"service-alerts-url"`
	RealTimeAuthHeaderName  string            `json:"realtime-auth-header-name"`
	RealTimeAuthHeaderValue string            `json:"realtime-auth-header-value"`
	Headers                 map[string]string `json:"headers"`
	RefreshInterval         int               `json:"refresh-interval"`
	Enabled                 *bool             `json:"enabled"`
}

// JSONConfig represents the JSON configuration file structure
type JSONConfig struct {
	Port             int            `json:"port"`
	Env              string         `json:"env"`
	ApiKeys          []string       `json:"api-keys"`
	ProtectedApiKeys []string       `json:"protected-api-keys"`
	ExemptApiKeys    []string       `json:"exempt-api-keys"`
	RateLimit        int            `json:"rate-limit"`
	GtfsStaticFeed   GtfsStaticFeed `json:"gtfs-static-feed"`
	GtfsRtFeeds      []GtfsRtFeed   `json:"gtfs-rt-feeds"`
	DataPath         string         `json:"data-path"`
}

// setDefaults applies default values to the JSON config if fields are missing or zero
func (j *JSONConfig) setDefaults() {
	if j.Port == 0 {
		j.Port = 4000
	}
	if j.Env == "" {
		j.Env = "development"
	}
	if len(j.ApiKeys) == 0 {
		j.ApiKeys = []string{"test"}
	}
	if len(j.ProtectedApiKeys) == 0 && (j.Env == "development" || j.Env == "test") {
		j.ProtectedApiKeys = []string{"protected-test-key"}
	}
	if len(j.ExemptApiKeys) == 0 {
		j.ExemptApiKeys = []string{"org.onebusaway.iphone"}
	}
	if j.RateLimit == 0 {
		j.RateLimit = 100
	}
	if j.GtfsStaticFeed.URL == "" {
		j.GtfsStaticFeed.URL = "https://www.soundtransit.org/GTFS-rail/40_gtfs.zip"
	}
	if len(j.GtfsRtFeeds) == 0 {
		j.GtfsRtFeeds = []GtfsRtFeed{
			{
				TripUpdatesURL:      "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/trip-updates-for-agency/40.pb?key=org.onebusaway.iphone",
				VehiclePositionsURL: "https://api.pugetsound.onebusaway.org/api/gtfs_realtime/vehicle-positions-for-agency/40.pb?key=org.onebusaway.iphone",
			},
		}
	}
	if j.DataPath == "" {
		j.DataPath = "./gtfs.db"
	}
}

// validate checks that the configuration is valid
func (j *JSONConfig) Validate() error {
	if j.Port < 1 || j.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", j.Port)
	}

	validEnvs := map[string]bool{
		"development": true,
		"test":        true,
		"production":  true,
	}
	if !validEnvs[j.Env] {
		return fmt.Errorf("env must be one of [development, test, production], got %q", j.Env)
	}

	if j.RateLimit < 1 {
		return fmt.Errorf("rate-limit must be at least 1, got %d", j.RateLimit)
	}

	if len(j.ApiKeys) == 0 {
		return fmt.Errorf("api-keys cannot be empty")
	}

	if len(j.ProtectedApiKeys) == 0 {
		return fmt.Errorf("protected-api-keys cannot be empty")
	}

	// Check for duplicate API keys
	seen := make(map[string]bool)
	for _, key := range j.ApiKeys {
		if key == "" {
			return fmt.Errorf("api-keys cannot contain empty strings")
		}
		if seen[key] {
			return fmt.Errorf("duplicate API key found: %q", key)
		}
		seen[key] = true
	}

	// Check for duplicate Protected API keys
	seenProtected := make(map[string]bool)
	for _, key := range j.ProtectedApiKeys {
		if key == "" {
			return fmt.Errorf("protected-api-keys cannot contain empty strings")
		}
		if seenProtected[key] {
			return fmt.Errorf("duplicate protected API key found: %q", key)
		}
		seenProtected[key] = true
	}

	// Validate DataPath for path traversal attempts
	if err := validatePath(j.DataPath, "data-path"); err != nil {
		return err
	}

	// Validate that both auth header fields are provided together or neither
	if (j.GtfsStaticFeed.AuthHeaderName != "" && j.GtfsStaticFeed.AuthHeaderValue == "") ||
		(j.GtfsStaticFeed.AuthHeaderName == "" && j.GtfsStaticFeed.AuthHeaderValue != "") {
		return fmt.Errorf("both auth-header-name and auth-header-value must be provided together for gtfs-static-feed")
	}

	// Validate GtfsStaticFeed.URL to prevent file:// URLs and other security issues
	if j.GtfsStaticFeed.URL != "" {
		// Block file:// URLs (case-insensitive)
		if strings.HasPrefix(strings.ToLower(j.GtfsStaticFeed.URL), "file://") {
			return fmt.Errorf("file:// URLs are not allowed for gtfs-static-feed.url for security reasons")
		}

		// For HTTP(S) URLs, no path checks needed
		if strings.HasPrefix(j.GtfsStaticFeed.URL, "http://") ||
			strings.HasPrefix(j.GtfsStaticFeed.URL, "https://") {
			return nil
		}

		// For file paths, validate for path traversal
		if err := validatePath(j.GtfsStaticFeed.URL, "gtfs-static-feed.url"); err != nil {
			return err
		}
	}

	return nil
}

// validatePath checks a file path for security issues
func validatePath(path, fieldName string) error {
	if path == "" {
		return nil
	}

	// Special case: allow :memory: for SQLite
	if path == ":memory:" {
		return nil
	}

	// Normalize the path
	cleanPath := filepath.Clean(path)

	// Check if the cleaned path tries to escape using ..
	if strings.HasPrefix(cleanPath, "..") {
		return fmt.Errorf("%s cannot start with '..' for security reasons", fieldName)
	}

	// Check for path traversal sequences in the middle of the path
	if strings.Contains(cleanPath, string(filepath.Separator)+".."+string(filepath.Separator)) ||
		strings.HasSuffix(cleanPath, string(filepath.Separator)+"..") {
		return fmt.Errorf("%s cannot contain path traversal sequences for security reasons", fieldName)
	}

	return nil
}

// ToAppConfig converts JSONConfig to appconf.Config
func (j *JSONConfig) ToAppConfig() Config {
	return Config{
		Port:             j.Port,
		Env:              EnvFlagToEnvironment(j.Env),
		ApiKeys:          j.ApiKeys,
		ProtectedApiKeys: j.ProtectedApiKeys,
		ExemptApiKeys:    j.ExemptApiKeys,
		Verbose:          true, // Always set to true like in main.go
		RateLimit:        j.RateLimit,
	}
}

// RTFeedConfigData holds per-feed GTFS-RT configuration
type RTFeedConfigData struct {
	ID                  string   // Note it's will be generated if missing
	AgencyIDs           []string // When set, only realtime data for these agencies is included
	TripUpdatesURL      string
	VehiclePositionsURL string
	ServiceAlertsURL    string
	Headers             map[string]string
	RefreshInterval     int  // seconds, default 30
	Enabled             bool // default true
}

// GtfsConfigData holds GTFS configuration data without importing gtfs package
// This avoids import cycles
type GtfsConfigData struct {
	GtfsURL               string
	StaticAuthHeaderKey   string
	StaticAuthHeaderValue string
	RTFeeds               []RTFeedConfigData
	GTFSDataPath          string
	Env                   Environment
	Verbose               bool
	EnableGTFSTidy        bool
}

// ToGtfsConfigData converts JSONConfig to GtfsConfigData
func (j *JSONConfig) ToGtfsConfigData() (GtfsConfigData, error) {
	cfg := GtfsConfigData{
		GtfsURL:               j.GtfsStaticFeed.URL,
		StaticAuthHeaderKey:   j.GtfsStaticFeed.AuthHeaderName,
		StaticAuthHeaderValue: j.GtfsStaticFeed.AuthHeaderValue,
		GTFSDataPath:          j.DataPath,
		Env:                   EnvFlagToEnvironment(j.Env),
		Verbose:               true, // Always set to true like in main.go
		EnableGTFSTidy:        j.GtfsStaticFeed.EnableGTFSTidy,
	}

	for i, feed := range j.GtfsRtFeeds {
		feedID := feed.ID
		if feedID == "" {
			feedID = fmt.Sprintf("feed-%d", i)
		}

		for _, existingID := range cfg.RTFeeds {
			if existingID.ID == feedID {
				return GtfsConfigData{}, fmt.Errorf("duplicate feed ID found: %q", feedID)
			}
		}

		headers := make(map[string]string)
		for k, v := range feed.Headers {
			headers[k] = v
		}
		if feed.RealTimeAuthHeaderName != "" && feed.RealTimeAuthHeaderValue != "" {
			if _, exists := headers[feed.RealTimeAuthHeaderName]; !exists {
				headers[feed.RealTimeAuthHeaderName] = feed.RealTimeAuthHeaderValue
			} else {
				slog.Warn("Legacy auth header key conflicts with headers map; legacy value discarded",
					slog.String("feed", feedID),
					slog.String("header", feed.RealTimeAuthHeaderName),
				)
			}
		} else if feed.RealTimeAuthHeaderName != "" || feed.RealTimeAuthHeaderValue != "" {
			slog.Warn("Legacy realtime-auth-header-name and realtime-auth-header-value must both be set; ignoring incomplete pair",
				slog.String("feed", feedID),
			)
		}

		// Default refresh interval is 30 seconds
		refreshInterval := feed.RefreshInterval
		if refreshInterval <= 0 {
			refreshInterval = 30
		}

		// Default enabled to true
		enabled := true
		if feed.Enabled != nil {
			enabled = *feed.Enabled
		}

		cfg.RTFeeds = append(cfg.RTFeeds, RTFeedConfigData{
			ID:                  feedID,
			AgencyIDs:           feed.AgencyIDs,
			TripUpdatesURL:      feed.TripUpdatesURL,
			VehiclePositionsURL: feed.VehiclePositionsURL,
			ServiceAlertsURL:    feed.ServiceAlertsURL,
			Headers:             headers,
			RefreshInterval:     refreshInterval,
			Enabled:             enabled,
		})
	}

	return cfg, nil
}

// LoadFromFile loads configuration from a JSON file
func LoadFromFile(path string) (*JSONConfig, error) {
	logger := slog.Default().With("config_file", path)
	logger.Debug("loading configuration file")

	// Use Lstat to prevent symlink attacks
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat config file: %w", err)
	}

	// Check if it's a regular file (not a symlink, directory, or device)
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("config file must be a regular file, not a %s", info.Mode().Type())
	}

	// Check file size to prevent loading extremely large files
	const maxConfigSize = 10 * 1024 * 1024 // 10MB limit
	if info.Size() > maxConfigSize {
		return nil, fmt.Errorf("config file too large: %d bytes (max: %d)", info.Size(), maxConfigSize)
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse JSON
	var config JSONConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	// Apply defaults
	config.setDefaults()

	// Override API Keys (Split by comma, trim spaces, ignore empty)
	if envKeys := os.Getenv("GTFS_API_KEYS"); envKeys != "" {
		rawKeys := strings.Split(envKeys, ",")
		var cleanKeys []string
		for _, k := range rawKeys {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				cleanKeys = append(cleanKeys, trimmed)
			}
		}
		if len(cleanKeys) > 0 {
			config.ApiKeys = cleanKeys
		}
	}

	// Override Protected API Keys
	if envProtectedKeys := os.Getenv("GTFS_PROTECTED_API_KEYS"); envProtectedKeys != "" {
		rawKeys := strings.Split(envProtectedKeys, ",")
		var cleanKeys []string
		for _, k := range rawKeys {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				cleanKeys = append(cleanKeys, trimmed)
			}
		}
		if len(cleanKeys) > 0 {
			config.ProtectedApiKeys = cleanKeys
		}
	}

	// Override Static Feed Auth (Name + Value)
	if staticName := os.Getenv("GTFS_STATIC_AUTH_NAME"); staticName != "" {
		config.GtfsStaticFeed.AuthHeaderName = staticName
	}
	if staticValue := os.Getenv("GTFS_STATIC_AUTH_VALUE"); staticValue != "" {
		config.GtfsStaticFeed.AuthHeaderValue = staticValue
	}

	// Override Realtime Feed Auth (Name + Value)
	// Note: Currently only overrides the first configured realtime feed explicitly
	rtName := os.Getenv("GTFS_REALTIME_AUTH_NAME")
	rtValue := os.Getenv("GTFS_REALTIME_AUTH_VALUE")

	if rtName != "" || rtValue != "" {
		if len(config.GtfsRtFeeds) > 0 {
			if rtName != "" {
				config.GtfsRtFeeds[0].RealTimeAuthHeaderName = rtName
			}
			if rtValue != "" {
				config.GtfsRtFeeds[0].RealTimeAuthHeaderValue = rtValue
			}
		} else {
			slog.Warn("GTFS_REALTIME_AUTH env vars set but no Realtime feeds configured",
				"component", "config_loader")
		}
	}

	// Validate
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	logger.Debug("configuration loaded successfully",
		"port", config.Port,
		"env", config.Env,
		"api_keys_count", len(config.ApiKeys),
		"rate_limit", config.RateLimit)

	return &config, nil
}
