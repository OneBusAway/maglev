package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockAddTrip must leave a timed trip with cached time bounds: a trip with
// timed stop_times and NULL bounds reads as flex-only and is skipped by the
// fixed-route queries.
func TestMockAddTrip_KeepsTimeBoundsForTimedTrips(t *testing.T) {
	manager := newFlexTestManager(t, "raba.zip")
	ctx := context.Background()
	var tripID, routeID string
	require.NoError(t, manager.GtfsDB.DB.QueryRowContext(ctx,
		"SELECT id, route_id FROM trips WHERE min_arrival_time IS NOT NULL ORDER BY id LIMIT 1").Scan(&tripID, &routeID))

	manager.MockAddTrip(tripID, "25", routeID)

	trip, err := manager.GtfsDB.Queries.GetTrip(ctx, tripID)
	require.NoError(t, err)
	assert.True(t, trip.MinArrivalTime.Valid, "a trip with timed stop_times keeps its min arrival time")
	assert.True(t, trip.MaxDepartureTime.Valid, "and its max departure time")
}
