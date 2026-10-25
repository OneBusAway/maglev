package appconf

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// defaultStaticFeedURL is used when no static feed key is configured.
const defaultStaticFeedURL = "https://www.soundtransit.org/GTFS-rail/40_gtfs.zip"

// GtfsStaticFeed represents one static GTFS feed configuration
type GtfsStaticFeed struct {
	ID               string            `json:"id,omitempty"`
	URL              string            `json:"url"`
	DefaultAgencyID  string            `json:"default-agency-id,omitempty"`
	AgencyIDMappings map[string]string `json:"agency-id-mappings,omitempty"`
	AuthHeaderName   string            `json:"auth-header-name,omitempty"`
	AuthHeaderValue  string            `json:"auth-header-value,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	EnableGTFSTidy   bool              `json:"enable-gtfs-tidy,omitempty"`
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
	Port             int      `json:"port"`
	Host             string   `json:"host"`
	Env              string   `json:"env"`
	ApiKeys          []string `json:"api-keys"`
	ProtectedApiKeys []string `json:"protected-api-keys"`
	ExemptApiKeys    []string `json:"exempt-api-keys"`
	RateLimit        int      `json:"rate-limit"`
	// GtfsStaticFeed is the legacy single-feed key. It is a pointer so an
	// absent key can be told apart from an empty one.
	GtfsStaticFeed *GtfsStaticFeed `json:"gtfs-static-feed"`
	// GtfsStaticFeeds is nil when the key is absent and empty for "[]".
	GtfsStaticFeeds []GtfsStaticFeed `json:"gtfs-static-feeds"`
	GtfsRtFeeds     []GtfsRtFeed     `json:"gtfs-rt-feeds"`
	DataPath        string           `json:"data-path"`
	LogLevel        string           `json:"log-level"`
	LogFormat       string           `json:"log-format"`
	TLSCertPath     string           `json:"tls-cert-path"`
	TLSKeyPath      string           `json:"tls-key-path"`

	// staticFeedsKey names the key GtfsStaticFeeds came from, for errors.
	staticFeedsKey string
}

const (
	legacyStaticFeedKey = "gtfs-static-feed"
	staticFeedListKey   = "gtfs-static-feeds"
)

// normalizeStaticFeeds moves the legacy gtfs-static-feed key into
// GtfsStaticFeeds, records which key the feeds came from and fills in
// missing feed IDs. Calling it again is harmless.
func (j *JSONConfig) normalizeStaticFeeds() error {
	if j.GtfsStaticFeed != nil && j.GtfsStaticFeeds != nil {
		return fmt.Errorf("gtfs-static-feed and gtfs-static-feeds cannot both be set")
	}
	if j.GtfsStaticFeed != nil {
		j.GtfsStaticFeeds = []GtfsStaticFeed{*j.GtfsStaticFeed}
		j.GtfsStaticFeed = nil
		j.staticFeedsKey = legacyStaticFeedKey
	}
	if j.GtfsStaticFeeds != nil && j.staticFeedsKey == "" {
		j.staticFeedsKey = staticFeedListKey
	}
	for i := range j.GtfsStaticFeeds {
		j.GtfsStaticFeeds[i].ID = feedIDOrDefault(i, j.GtfsStaticFeeds[i].ID)
	}
	return nil
}

// feedIDOrDefault returns id, or "feed-{index}" when id is empty.
func feedIDOrDefault(index int, id string) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("feed-%d", index)
}

// DefaultProtectedAPIKeys returns the protected API keys used when none are
// configured. Only development and test get a default; production must
// supply its own keys.
func DefaultProtectedAPIKeys(env string) []string {
	if env == "development" || env == "test" {
		return []string{"protected-test-key"}
	}
	return nil
}

// ProtectedAPIKeysFromEnv returns the comma-separated keys in
// GTFS_PROTECTED_API_KEYS with surrounding whitespace and empty entries
// removed, or nil when none are set.
func ProtectedAPIKeysFromEnv() []string {
	var keys []string
	for _, key := range strings.Split(os.Getenv("GTFS_PROTECTED_API_KEYS"), ",") {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return keys
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
	if len(j.ProtectedApiKeys) == 0 {
		j.ProtectedApiKeys = DefaultProtectedAPIKeys(j.Env)
	}
	if len(j.ExemptApiKeys) == 0 {
		j.ExemptApiKeys = []string{"org.onebusaway.iphone"}
	}
	if j.RateLimit == 0 {
		j.RateLimit = 100
	}
	j.setStaticFeedDefaults()
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
	if j.LogLevel == "" {
		j.LogLevel = "info"
	}
	if j.LogFormat == "" {
		j.LogFormat = "text"
	}
}

// setStaticFeedDefaults adds the default feed when no static feed key is
// set, and gives the legacy single feed the default URL when it has none.
// A gtfs-static-feeds list is left alone. It expects normalized feeds.
func (j *JSONConfig) setStaticFeedDefaults() {
	if j.GtfsStaticFeeds == nil {
		j.GtfsStaticFeeds = []GtfsStaticFeed{{ID: feedIDOrDefault(0, ""), URL: defaultStaticFeedURL}}
		j.staticFeedsKey = legacyStaticFeedKey
		return
	}
	if j.staticFeedsKey == legacyStaticFeedKey && j.GtfsStaticFeeds[0].URL == "" {
		j.GtfsStaticFeeds[0].URL = defaultStaticFeedURL
	}
}

// validate checks that the configuration is valid
func (j *JSONConfig) Validate() error {
	if err := j.normalizeStaticFeeds(); err != nil {
		return err
	}

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

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[j.LogLevel] {
		return fmt.Errorf("log level must be one of [debug, info, warn, error], got %q", j.LogLevel)
	}

	validLogFormats := map[string]bool{
		"text": true,
		"json": true,
	}
	if !validLogFormats[j.LogFormat] {
		return fmt.Errorf("log format must be one of [text, json], got %q", j.LogFormat)
	}

	// Validate DataPath for path traversal attempts
	if err := validatePath(j.DataPath, "data-path"); err != nil {
		return err
	}

	// TLS: both cert and key must be provided together
	if (j.TLSCertPath != "" && j.TLSKeyPath == "") || (j.TLSCertPath == "" && j.TLSKeyPath != "") {
		return fmt.Errorf("both tls-cert-path and tls-key-path must be provided together")
	}
	if err := validatePath(j.TLSCertPath, "tls-cert-path"); err != nil {
		return err
	}
	if err := validatePath(j.TLSKeyPath, "tls-key-path"); err != nil {
		return err
	}

	return j.validateStaticFeeds()
}

// validateStaticFeeds applies the configuration-time checks to the
// normalized static feed list: a non-empty list, unique ids and default
// agencies, and valid fields in each feed.
func (j *JSONConfig) validateStaticFeeds() error {
	if j.GtfsStaticFeeds != nil && len(j.GtfsStaticFeeds) == 0 {
		return fmt.Errorf("gtfs-static-feeds cannot be empty")
	}

	seenIDs := make(map[string]bool)
	seenDefaultAgencyIDs := make(map[string]bool)
	for _, feed := range j.GtfsStaticFeeds {
		if seenIDs[feed.ID] {
			return fmt.Errorf("duplicate %s id: %q", j.staticFeedsKey, feed.ID)
		}
		seenIDs[feed.ID] = true

		if feed.DefaultAgencyID != "" {
			if seenDefaultAgencyIDs[feed.DefaultAgencyID] {
				return fmt.Errorf("duplicate %s default-agency-id: %q", j.staticFeedsKey, feed.DefaultAgencyID)
			}
			seenDefaultAgencyIDs[feed.DefaultAgencyID] = true
		}

		if err := feed.validate(); err != nil {
			return fmt.Errorf("%s %q: %w", j.staticFeedsKey, feed.ID, err)
		}
	}
	return nil
}

// validate checks one static feed's agency IDs, auth pair and URL. Agency
// IDs must not contain "_", which separates the agency from the entity in
// combined IDs.
func (feed GtfsStaticFeed) validate() error {
	if strings.Contains(feed.DefaultAgencyID, "_") {
		return fmt.Errorf("default-agency-id %q must not contain '_'", feed.DefaultAgencyID)
	}
	for _, from := range slices.Sorted(maps.Keys(feed.AgencyIDMappings)) {
		to := feed.AgencyIDMappings[from]
		if from == "" {
			return fmt.Errorf("agency-id-mappings key for %q cannot be empty", to)
		}
		if to == "" {
			return fmt.Errorf("agency-id-mappings target for %q cannot be empty", from)
		}
		if strings.Contains(to, "_") {
			return fmt.Errorf("agency-id-mappings target %q for %q must not contain '_'", to, from)
		}
	}

	if (feed.AuthHeaderName != "") != (feed.AuthHeaderValue != "") {
		return fmt.Errorf("both auth-header-name and auth-header-value must be provided together")
	}

	if feed.URL == "" {
		return fmt.Errorf("url is required")
	}
	if strings.HasPrefix(strings.ToLower(feed.URL), "file://") {
		return fmt.Errorf("file:// URLs are not allowed in url for security reasons")
	}
	isHTTPURL := strings.HasPrefix(feed.URL, "http://") || strings.HasPrefix(feed.URL, "https://")
	if isHTTPURL {
		return nil
	}
	return validatePath(feed.URL, "url")
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
		Host:             j.Host,
		Env:              EnvFlagToEnvironment(j.Env),
		ApiKeys:          j.ApiKeys,
		ProtectedApiKeys: j.ProtectedApiKeys,
		ExemptApiKeys:    j.ExemptApiKeys,
		RateLimit:        j.RateLimit,
		LogLevel:         j.LogLevel,
		LogFormat:        j.LogFormat,
		TLSCertPath:      j.TLSCertPath,
		TLSKeyPath:       j.TLSKeyPath,
	}
}

// RTFeedConfigData holds per-feed GTFS-RT configuration
type RTFeedConfigData struct {
	ID                  string   // Note it will be generated if missing
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
	EnableGTFSTidy        bool
}

// ToGtfsConfigData converts JSONConfig to GtfsConfigData
func (j *JSONConfig) ToGtfsConfigData() (GtfsConfigData, error) {
	if err := j.normalizeStaticFeeds(); err != nil {
		return GtfsConfigData{}, err
	}

	cfg := GtfsConfigData{
		GTFSDataPath: j.DataPath,
		Env:          EnvFlagToEnvironment(j.Env),
	}
	// The importer still loads only the first static feed.
	feeds := j.GtfsStaticFeeds
	warnIgnoredStaticFeeds(feeds)
	if len(feeds) > 0 {
		warnUnusedStaticFeedFields(feeds[0])
		cfg.GtfsURL = feeds[0].URL
		cfg.StaticAuthHeaderKey = feeds[0].AuthHeaderName
		cfg.StaticAuthHeaderValue = feeds[0].AuthHeaderValue
		cfg.EnableGTFSTidy = feeds[0].EnableGTFSTidy
	}

	seen := make(map[string]struct{})

	for i, feed := range j.GtfsRtFeeds {
		feedID := feedIDOrDefault(i, feed.ID)

		if _, exists := seen[feedID]; exists {
			return GtfsConfigData{}, fmt.Errorf("duplicate feed ID found: %q", feedID)
		}
		seen[feedID] = struct{}{}

		headers := make(map[string]string, len(feed.Headers))
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

// warnIgnoredStaticFeeds logs the static feeds after the first, which the
// importer skips until multi-feed import lands.
func warnIgnoredStaticFeeds(feeds []GtfsStaticFeed) {
	if len(feeds) <= 1 {
		return
	}
	ignoredIDs := make([]string, 0, len(feeds)-1)
	for _, feed := range feeds[1:] {
		ignoredIDs = append(ignoredIDs, feed.ID)
	}
	slog.Warn("only the first static feed is loaded until multi-feed import lands",
		"component", "config_loader",
		"loaded_feed", feeds[0].ID,
		"ignored_feeds", ignoredIDs)
}

// warnUnusedStaticFeedFields logs the fields of the loaded static feed that
// the importer does not apply yet, so a config that sets them does not fail
// silently at fetch or import time.
func warnUnusedStaticFeedFields(feed GtfsStaticFeed) {
	// default-agency-id is not listed: setting it now is recommended, and the
	// example configs do.
	var unused []string
	if len(feed.AgencyIDMappings) > 0 {
		unused = append(unused, "agency-id-mappings")
	}
	if len(feed.Headers) > 0 {
		unused = append(unused, "headers")
	}
	if len(unused) == 0 {
		return
	}
	slog.Warn("static feed fields are accepted but not applied until multi-feed import lands",
		"component", "config_loader",
		"feed", feed.ID,
		"unused_fields", unused)
}

// applyStaticAuthEnvOverrides sets the static feed auth header from
// GTFS_STATIC_AUTH_NAME and GTFS_STATIC_AUTH_VALUE. The variables name no
// feed, so with more than one static feed they are an error: dropping a
// deployment secret silently would leave the feed fetching unauthenticated.
func (j *JSONConfig) applyStaticAuthEnvOverrides() error {
	name := os.Getenv("GTFS_STATIC_AUTH_NAME")
	value := os.Getenv("GTFS_STATIC_AUTH_VALUE")
	if name == "" && value == "" {
		return nil
	}

	if len(j.GtfsStaticFeeds) == 0 {
		return nil // Validate rejects the empty list
	}
	if len(j.GtfsStaticFeeds) > 1 {
		return fmt.Errorf("GTFS_STATIC_AUTH_NAME/GTFS_STATIC_AUTH_VALUE apply to one static feed, but %d are configured; set auth-header-name and auth-header-value on each feed instead", len(j.GtfsStaticFeeds))
	}
	feed := &j.GtfsStaticFeeds[0]
	if name != "" {
		feed.AuthHeaderName = name
	}
	if value != "" {
		feed.AuthHeaderValue = value
	}
	return nil
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
	if err := config.normalizeStaticFeeds(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
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

	// Override logging level and format
	if logLevel := strings.TrimSpace(os.Getenv("MAGLEV_LOG_LEVEL")); logLevel != "" {
		config.LogLevel = strings.ToLower(logLevel)
	}
	if logFormat := strings.TrimSpace(os.Getenv("MAGLEV_LOG_FORMAT")); logFormat != "" {
		config.LogFormat = strings.ToLower(logFormat)
	}

	// Override Protected API Keys
	if envProtectedKeys := ProtectedAPIKeysFromEnv(); len(envProtectedKeys) > 0 {
		config.ProtectedApiKeys = envProtectedKeys
	}

	if err := config.applyStaticAuthEnvOverrides(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
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
		"rate_limit", config.RateLimit,
		"log_level", config.LogLevel,
		"log_format", config.LogFormat)

	return &config, nil
}
