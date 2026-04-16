package gtfs

import (
	"time"

	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/metrics"
)

// Configuration for a single GTFS-RT feed.
type RTFeedConfig struct {
	ID                  string
	AgencyIDs           []string // When set, only realtime data for these agencies is included
	TripUpdatesURL      string
	VehiclePositionsURL string
	ServiceAlertsURL    string
	Headers             map[string]string
	RefreshInterval     int // seconds, default 30
	Enabled             bool
}

// Config holds GTFS configuration for the manager.
type Config struct {
	GtfsURL               string
	StaticAuthHeaderKey   string
	StaticAuthHeaderValue string
	RTFeeds               []RTFeedConfig
	GTFSDataPath          string
	// runningLateWindow and runningEarlyWindow tune trips-for-route time range selection.
	RunningLateWindow  time.Duration
	RunningEarlyWindow time.Duration
	Env                appconf.Environment
	Verbose            bool
	EnableGTFSTidy     bool
	StartupRetries     []time.Duration
	Metrics            *metrics.Metrics
}

const (
	// DefaultRunningLateWindow is how far behind "now" trips-for-route looks for still-relevant late trips.
	DefaultRunningLateWindow = 30 * time.Minute
	// DefaultRunningEarlyWindow is how far ahead "now" trips-for-route looks for early trips.
	DefaultRunningEarlyWindow = 10 * time.Minute
)

// enabledFeeds returns only the enabled feeds that have at least one URL configured.
func (config Config) enabledFeeds() []RTFeedConfig {
	var feeds []RTFeedConfig
	for _, feed := range config.RTFeeds {
		if feed.Enabled && (feed.TripUpdatesURL != "" || feed.VehiclePositionsURL != "" || feed.ServiceAlertsURL != "") {
			feeds = append(feeds, feed)
		}
	}
	return feeds
}
