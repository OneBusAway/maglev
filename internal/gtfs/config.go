package gtfs

import (
	"strings"
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
	Env                   appconf.Environment
	EnableGTFSTidy        bool
	StartupRetries        []time.Duration
	Metrics               *metrics.Metrics
}

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

// feedAgencyFilters maps each enabled feed's ID to its configured agency-ids.
// Disabled feeds are left out: they never poll, so an entry for one would
// make the metrics endpoint report its agencies as permanently unknown.
// Feeds without agency-ids get no entry, meaning they cover every agency.
func (config Config) feedAgencyFilters() map[string]map[string]bool {
	filters := make(map[string]map[string]bool)
	for _, feed := range config.enabledFeeds() {
		if len(feed.AgencyIDs) == 0 {
			continue
		}
		filter := make(map[string]bool, len(feed.AgencyIDs))
		for _, id := range feed.AgencyIDs {
			filter[id] = true
		}
		filters[feed.ID] = filter
	}
	return filters
}

func (config Config) isLocalFile() bool {
	return !strings.HasPrefix(config.GtfsURL, "http://") && !strings.HasPrefix(config.GtfsURL, "https://")
}
