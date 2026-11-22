package restapi

import (
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
)

func TestPreferredPositionVehicle_IgnoresRetainedVehicles(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	stopID, _, firstID, _ := setupDelayPropTestData(t, api, 1)
	addDownstreamStop(t, api, firstID)
	nextID := addLaterBlockTrip(t, api, stopID, firstID)
	delay := 10 * time.Minute
	live := gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "live"}, Trip: &gtfs.Trip{ID: gtfs.TripID{ID: firstID}}, Timestamp: &now}
	gone := gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "gone"}, Trip: &gtfs.Trip{ID: gtfs.TripID{ID: nextID}}, Timestamp: &now}
	// Equal timestamps ensure feed membership, not GPS age, decides ownership.
	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{live, gone}, "gone")
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{ID: gtfs.TripID{ID: nextID}, Timestamp: &now, Delay: &delay})
	require.Len(t, api.GtfsManager.GetRealTimeVehicles(), 2)
	require.Len(t, api.GtfsManager.GetLiveRealTimeVehicles(), 1)
	vehicle := api.preferredPositionVehicle(t.Context(), nextID)
	require.NotNil(t, vehicle)
	assert.Equal(t, "live", vehicle.ID.ID)
	vehicle = api.vehicleForTrip(t.Context(), nextID, serviceDate)
	require.NotNil(t, vehicle)
	assert.Equal(t, "live", vehicle.ID.ID)
	_, liveExtras, err := api.BuildTripStatus(t.Context(), "dp-agency", firstID, &live, serviceDate, now, nil)
	require.NoError(t, err)
	assert.True(t, liveExtras.record.present)
	_, extras, err := api.BuildTripStatus(t.Context(), "dp-agency", nextID, &gone, serviceDate, now, nil)
	require.NoError(t, err)
	assert.False(t, extras.record.present, "a retained bus must not consume a live bus's anonymous record")

	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{gone}, "gone")
	assert.Empty(t, api.GtfsManager.GetLiveRealTimeVehicles())
	assert.Nil(t, api.preferredPositionVehicle(t.Context(), nextID))
}

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
