package gtfs

import (
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// FeedAlert carries the publishing feed's first configured agency and the
// successful fetch/parse time through the immutable alert indexes.
type FeedAlert struct {
	gtfs.Alert
	FeedAgencyID string
	CreatedAt    time.Time
}

// storeFeedAlerts replaces one feed's alerts and timestamp together. The caller
// must hold realTimeMutex and publish a new merged snapshot afterward.
func (manager *Manager) storeFeedAlerts(feedID string, alerts []gtfs.Alert, ingestedAt time.Time) {
	manager.feedAlerts[feedID] = alerts
	manager.feedAlertIngestedAt[feedID] = ingestedAt
}
