package restapi

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

func TestPreferredPositionVehicle_BlockAssignment(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	stopID, _, firstID, _ := setupDelayPropTestData(t, api, 1)
	nextID := addLaterBlockTrip(t, api, stopID, firstID)
	blocklessID := firstID + "-blockless"
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(t.Context(), firstID)
	require.NoError(t, err)
	_, err = api.GtfsManager.GtfsDB.Queries.CreateTrip(t.Context(), gtfsdb.CreateTripParams{
		ID: blocklessID, RouteID: trip.RouteID, ServiceID: trip.ServiceID,
	})
	require.NoError(t, err)
	position := func(id, tripID string) gtfs.Vehicle {
		return gtfs.Vehicle{ID: &gtfs.VehicleID{ID: id}, Trip: &gtfs.Trip{ID: gtfs.TripID{ID: tripID}}}
	}
	first, next := position("first", firstID), position("next", nextID)
	updateOnly := position("update-only", nextID)
	updateOnly.Trip.IsEntityInMessage = true
	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{
		first, next, position("first", blocklessID), position("blockless", blocklessID),
		{}, {ID: &gtfs.VehicleID{ID: "no-trip"}}, position("", nextID),
		position("missing", "missing-trip"), updateOnly,
	})
	ctx := WithSnapshotCache(t.Context(), newSnapshotCache())
	for _, id := range []string{firstID, nextID} {
		vehicle := api.preferredPositionVehicle(ctx, id)
		require.NotNil(t, vehicle)
		assert.Equal(t, "next", vehicle.ID.ID)
	}
	vehicle := api.preferredPositionVehicle(ctx, blocklessID)
	require.NotNil(t, vehicle)
	assert.Equal(t, "blockless", vehicle.ID.ID)
	assert.Nil(t, api.preferredPositionVehicle(ctx, "missing-trip"))

	// A refresh must not change an assignment already shared by one request.
	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{next, first})
	assert.Equal(t, "next", api.preferredPositionVehicle(ctx, nextID).ID.ID)
	assert.Equal(t, "first", api.preferredPositionVehicle(t.Context(), nextID).ID.ID)
}
