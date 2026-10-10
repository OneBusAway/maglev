package gtfs

import (
	"cmp"
	"slices"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// tripUpdateRef is what the GTFS-RT exports need from one trip update: the
// trip, route and vehicle it names and its own timestamp. It is taken before
// the agency filter runs, so the export sees every update the feed sent.
type tripUpdateRef struct {
	TripID    string
	RouteID   string
	VehicleID string
	Timestamp *time.Time
}

// tripUpdateRefsInFeedOrder lists the trip updates among trips in the order
// the feed sent them. go-gtfs returns trips sorted by ID and records each
// update's feed position in EntityIndex; trips known only from vehicle
// positions or alerts are skipped.
func tripUpdateRefsInFeedOrder(trips []gtfs.Trip) []tripUpdateRef {
	updates := make([]gtfs.Trip, 0, len(trips))
	for _, trip := range trips {
		if trip.IsEntityInMessage {
			updates = append(updates, trip)
		}
	}
	slices.SortStableFunc(updates, func(a, b gtfs.Trip) int {
		return cmp.Compare(a.EntityIndex, b.EntityIndex)
	})

	refs := make([]tripUpdateRef, 0, len(updates))
	for _, trip := range updates {
		ref := tripUpdateRef{TripID: trip.ID.ID, RouteID: trip.ID.RouteID, Timestamp: trip.Timestamp}
		if trip.Vehicle != nil && trip.Vehicle.ID != nil {
			ref.VehicleID = trip.Vehicle.ID.ID
		}
		refs = append(refs, ref)
	}
	return refs
}

// tripUpdateMatchingReference is the legacy block-matching time for an
// update: its own timestamp when supplied (zero included), else the feed
// header's, else the Unix epoch. It is never the export request time.
// go-gtfs leaves CreatedAt as the zero time.Time when the header omits it.
func tripUpdateMatchingReference(ref tripUpdateRef, feedCreatedAt time.Time) time.Time {
	switch {
	case ref.Timestamp != nil:
		return *ref.Timestamp
	case !feedCreatedAt.IsZero():
		return feedCreatedAt
	default:
		return time.Unix(0, 0)
	}
}
