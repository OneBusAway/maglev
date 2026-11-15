package gtfs

import "github.com/OneBusAway/go-gtfs"

// FeedAlert wraps a gtfs.Alert with the first agency-ids entry configured for
// the realtime feed that published it. Java's GtfsRealtimeEntitySource
// qualifies entity IDs absent from the static feed with _agencyIds.get(0), so
// that agency has to travel with the alert into the merged snapshot.
// Embedding keeps alert.ID and alert.InformedEntities working as before.
type FeedAlert struct {
	gtfs.Alert
	FeedAgencyID string
}
