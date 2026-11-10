package gtfs

import (
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// FeedAlert carries an alert's ingestion time through the immutable indexes.
// CreatedAt is the successful fetch/parse time, not the feed header or read time.
type FeedAlert struct {
	gtfs.Alert
	CreatedAt time.Time
}

// storeFeedAlerts replaces one feed's alerts and timestamp together. The caller
// must hold realTimeMutex and publish a new merged snapshot afterward.
func (manager *Manager) storeFeedAlerts(feedID string, alerts []gtfs.Alert, ingestedAt time.Time) {
	if manager.feedAlerts == nil {
		manager.feedAlerts = make(map[string][]gtfs.Alert)
	}
	if manager.feedAlertIngestedAt == nil {
		manager.feedAlertIngestedAt = make(map[string]time.Time)
	}
	manager.feedAlerts[feedID] = alerts
	manager.feedAlertIngestedAt[feedID] = ingestedAt
}
