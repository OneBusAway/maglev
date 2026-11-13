package gtfs

import (
	"reflect"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// FeedAlert carries the publishing feed's first configured agency and the
// creation time of the alert's current content through the immutable indexes.
type FeedAlert struct {
	gtfs.Alert
	FeedAgencyID string
	CreatedAt    time.Time
}

// storeFeedAlerts retains creation times for unchanged alerts in the same feed.
// The caller holds realTimeMutex and publishes a new merged snapshot afterward.
func (manager *Manager) storeFeedAlerts(feedID string, alerts []gtfs.Alert, ingestedAt time.Time) {
	previous := make(map[string]gtfs.Alert, len(manager.feedAlerts[feedID]))
	for _, alert := range manager.feedAlerts[feedID] {
		previous[alert.ID] = alert
	}

	previousTimes := manager.feedAlertCreatedAt[feedID]
	createdAt := make(map[string]time.Time, len(alerts))
	for _, alert := range alerts {
		at := ingestedAt
		// The parsed alert contains content only; feed header and ingestion
		// timestamps live outside it.
		if oldAt := previousTimes[alert.ID]; !oldAt.IsZero() && sameAlertContent(previous[alert.ID], alert) {
			at = oldAt
		}
		createdAt[alert.ID] = at
	}
	manager.feedAlerts[feedID] = alerts
	manager.feedAlertCreatedAt[feedID] = createdAt
}

// sameAlertContent ignores selector order. The parser can append inferred
// routes in map iteration order, even when the source payload is unchanged.
func sameAlertContent(a, b gtfs.Alert) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	aSelectors, bSelectors := a.InformedEntities, b.InformedEntities
	a.InformedEntities, b.InformedEntities = nil, nil
	if len(aSelectors) != len(bSelectors) || !reflect.DeepEqual(a, b) {
		return false
	}

	remaining := slices.Clone(bSelectors)
	for _, selector := range aSelectors {
		match := slices.IndexFunc(remaining, func(candidate gtfs.AlertInformedEntity) bool {
			return reflect.DeepEqual(selector, candidate)
		})
		if match < 0 {
			return false
		}
		remaining[match] = remaining[len(remaining)-1]
		remaining = remaining[:len(remaining)-1]
	}
	return true
}
