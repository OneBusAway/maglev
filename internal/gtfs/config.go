package gtfs

import (
	"time"

	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
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

// Redacted returns a copy of the realtime feed config with source URLs and
// header values safe for operational logs. The original config remains intact
// for HTTP requests.
func (config RTFeedConfig) Redacted() RTFeedConfig {
	config.TripUpdatesURL = appconf.RedactURLForLog(config.TripUpdatesURL)
	config.VehiclePositionsURL = appconf.RedactURLForLog(config.VehiclePositionsURL)
	config.ServiceAlertsURL = appconf.RedactURLForLog(config.ServiceAlertsURL)
	config.Headers = appconf.RedactHeaderValues(config.Headers)
	return config
}

// Config holds GTFS configuration for the manager.
type Config struct {
	// gtfsLogURL is computed once when the manager is initialized. It is kept
	// separate from GtfsURL because the latter is used for fetching and feed identity.
	gtfsLogURL string
	// StaticFeeds lists every static feed in load order. StaticFeeds[0] is
	// authoritative: GtfsURL, StaticAuthHeader* and EnableGTFSTidy are
	// derived from it, and the importer still reads only those fields.
	StaticFeeds []appconf.GtfsStaticFeed
	// BundleInputsURL is the manifest StaticFeeds was read from, if any.
	BundleInputsURL       string
	GtfsURL               string
	StaticAuthHeaderKey   string
	StaticAuthHeaderValue string
	RTFeeds               []RTFeedConfig
	GTFSDataPath          string
	Env                   appconf.Environment
	EnableGTFSTidy        bool
	StartupRetries        []time.Duration
	Metrics               *metrics.Metrics
	Clock                 clock.Clock
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

// firstAgencyForFeed returns the first agency-ids entry configured for feedID.
// Java's GtfsRealtimeEntitySource qualifies entity IDs that are absent from
// the static feed with _agencyIds.get(0), so that agency has to travel with
// the feed's alerts. Maglev's agency-ids are optional; a feed without one has
// no configured first agency.
func (config Config) firstAgencyForFeed(feedID string) string {
	for _, feed := range config.RTFeeds {
		if feed.ID != feedID || len(feed.AgencyIDs) == 0 {
			continue
		}
		return feed.AgencyIDs[0]
	}
	return ""
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

// StaticLogSource returns the configured static feed source safe for logs.
// The raw GtfsURL remains unchanged for fetching and feed identity. Managers
// precompute gtfsLogURL once; standalone Config values use the safe fallback.
func (config Config) StaticLogSource() string {
	if config.gtfsLogURL != "" {
		return config.gtfsLogURL
	}
	return appconf.RedactURLForLog(config.GtfsURL)
}

func (config Config) isLocalFile() bool {
	return !appconf.IsHTTPURL(config.GtfsURL)
}
