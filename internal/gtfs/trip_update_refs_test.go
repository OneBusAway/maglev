package gtfs

import (
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func tripUpdate(tripID string, entityIndex int, vehicleID string, timestamp *time.Time) gtfs.Trip {
	trip := gtfs.Trip{
		ID:                gtfs.TripID{ID: tripID, RouteID: "R_" + tripID},
		Timestamp:         timestamp,
		EntityIndex:       entityIndex,
		IsEntityInMessage: true,
	}
	if vehicleID != "" {
		trip.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: vehicleID}}
	}
	return trip
}

func TestTripUpdateRefsInFeedOrder(t *testing.T) {
	stamp := time.Unix(100, 0)
	// go-gtfs returns trips sorted by ID; the feed sent T_B first.
	trips := []gtfs.Trip{
		tripUpdate("T_A", 2, "", nil),
		tripUpdate("T_B", 0, "V1", &stamp),
		{ID: gtfs.TripID{ID: "T_VEHICLE_ONLY"}}, // known only from a vehicle position
	}

	refs := tripUpdateRefsInFeedOrder(trips)

	assert.Equal(t, []tripUpdateRef{
		{TripID: "T_B", RouteID: "R_T_B", VehicleID: "V1", Timestamp: &stamp},
		{TripID: "T_A", RouteID: "R_T_A"},
	}, refs)
}

func TestTripUpdateMatchingReference(t *testing.T) {
	updateTime := time.Unix(100, 0)
	zero := time.Unix(0, 0)
	header := time.Unix(500, 0)

	assert.Equal(t, updateTime, tripUpdateMatchingReference(tripUpdateRef{Timestamp: &updateTime}, header))
	assert.Equal(t, zero, tripUpdateMatchingReference(tripUpdateRef{Timestamp: &zero}, header), "a supplied zero is used, not skipped")
	assert.Equal(t, header, tripUpdateMatchingReference(tripUpdateRef{}, header))
	assert.Equal(t, zero, tripUpdateMatchingReference(tripUpdateRef{}, time.Unix(0, 0)), "a zero header timestamp is supplied")
	assert.Equal(t, zero, tripUpdateMatchingReference(tripUpdateRef{}, time.Time{}), "an absent header falls back to the epoch")
}
