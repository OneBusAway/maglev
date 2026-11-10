package restapi

import (
	"github.com/OneBusAway/go-gtfs"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

// feedAlertsForTest wraps parser fixtures without inventing an ingestion time.
// Ingestion-time tests use the real fetch path instead.
func feedAlertsForTest(alerts []gtfs.Alert) []internalgtfs.FeedAlert {
	wrapped := make([]internalgtfs.FeedAlert, len(alerts))
	for i, alert := range alerts {
		wrapped[i] = internalgtfs.FeedAlert{Alert: alert}
	}
	return wrapped
}
