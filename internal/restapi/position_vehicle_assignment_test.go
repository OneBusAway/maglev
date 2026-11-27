package restapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

func TestPreferredPositionVehicle_IgnoresRetainedVehicles(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createIsolatedTestApiWithClock(t, clock.NewMockClock(now))
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
	vehicle := api.preferredPositionVehicle(t.Context(), nextID, serviceDate)
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
	assert.Nil(t, api.preferredPositionVehicle(t.Context(), nextID, serviceDate))
}

func TestPreferredPositionVehicle_SeparateShifts(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createIsolatedTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	stopID, _, firstID, _ := setupDelayPropTestData(t, api, 1)
	addDownstreamStop(t, api, firstID)
	nextID := addLaterBlockTrip(t, api, stopID, firstID)
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(t.Context(), firstID)
	require.NoError(t, err)
	overlapID := firstID + "-overlap"
	_, err = api.GtfsManager.GtfsDB.Queries.CreateTrip(t.Context(), gtfsdb.CreateTripParams{
		ID: overlapID, RouteID: trip.RouteID, ServiceID: trip.ServiceID, BlockID: trip.BlockID,
	})
	require.NoError(t, err)
	for i, at := range []time.Duration{10*time.Hour + 30*time.Minute, 12 * time.Hour} {
		_, err = api.GtfsManager.GtfsDB.Queries.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
			TripID: overlapID, StopID: stopID, StopSequence: int64(i + 1), ArrivalTime: int64(at), DepartureTime: int64(at),
		})
		require.NoError(t, err)
	}
	require.NoError(t, api.GtfsManager.GtfsDB.Queries.BulkUpdateTripTimeBounds(t.Context()))
	api.GtfsManager.MockAddVehicleWithOptions("first-shift", firstID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
	api.GtfsManager.MockAddVehicleWithOptions("second-shift", overlapID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
	ctx := WithSnapshotCache(t.Context(), newSnapshotCache())
	require.Equal(t, []string{firstID, nextID}, api.blockShiftTripIDsSortedByStartTime(ctx, nextID, serviceDate))
	require.Equal(t, []string{overlapID}, api.blockShiftTripIDsSortedByStartTime(ctx, overlapID, serviceDate))
	for i, id := range []string{nextID, overlapID} {
		recordTime := now.Add(time.Duration(i-1) * time.Minute)
		delay := time.Duration(i*9+1) * time.Minute
		api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{ID: gtfs.TripID{ID: id}, Timestamp: &recordTime, Delay: &delay})
	}
	for i, id := range []string{nextID, overlapID} {
		wantID := []string{"first-shift", "second-shift"}[i]
		vehicle := api.vehicleForTrip(ctx, id, serviceDate)
		require.NotNil(t, vehicle)
		assert.Equal(t, wantID, vehicle.ID.ID)
		status, extras, err := api.BuildTripStatus(ctx, "dp-agency", id, nil, serviceDate, now, nil)
		require.NoError(t, err)
		assert.True(t, extras.record.present)
		assert.Equal(t, "dp-agency_"+wantID, status.VehicleID)
		assert.Equal(t, now.Add(time.Duration(i-1)*time.Minute).UnixMilli(), status.LastUpdateTime.UnixMilli())
		assert.Equal(t, i*540+60, status.ScheduleDeviation)
		update, err := api.GtfsManager.GetTripUpdateByID(id)
		require.NoError(t, err)
		updates := api.predictionRecordUpdates(ctx, *update, id, serviceDate)
		require.Len(t, updates, 1)
		assert.Equal(t, id, updates[0].tripID)
		combinedTripID := "dp-agency_" + id
		resp, single := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
			"/api/where/arrival-and-departure-for-stop/dp-agency_%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
			stopID, combinedTripID, serviceDate.UnixMilli()))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "dp-agency_"+wantID, single.Data.Entry.VehicleID)
		assert.Equal(t, status.LastUpdateTime.UnixMilli(), single.Data.Entry.LastUpdateTime.UnixMilli())
		resp, plural := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
			"/api/where/arrivals-and-departures-for-stop/dp-agency_%s.json?key=TEST&minutesBefore=60&minutesAfter=300", stopID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		arrival := findArrivalForTrip(t, plural.Data.Entry.ArrivalsAndDepartures, combinedTripID)
		assert.Equal(t, "dp-agency_"+wantID, arrival.VehicleID)
		assert.Equal(t, status.LastUpdateTime.UnixMilli(), arrival.LastUpdateTime.UnixMilli())
	}
}

func TestPreferredPositionVehicle_SeparateServiceDates(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createIsolatedTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	position := func(id string, date time.Time) gtfs.Vehicle {
		return gtfs.Vehicle{ID: &gtfs.VehicleID{ID: id}, Trip: &gtfs.Trip{
			ID: gtfs.TripID{ID: tripID, HasStartDate: true, StartDate: date},
		}}
	}
	tomorrow := serviceDate.AddDate(0, 0, 1)
	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{
		position("tomorrow", tomorrow), position("current", serviceDate),
	})
	ctx := WithSnapshotCache(t.Context(), newSnapshotCache())
	for _, tt := range []struct {
		date time.Time
		id   string
	}{{serviceDate, "current"}, {tomorrow, "tomorrow"}, {serviceDate, "current"}} {
		vehicle := api.preferredPositionVehicle(ctx, tripID, tt.date)
		require.NotNil(t, vehicle)
		assert.Equal(t, tt.id, vehicle.ID.ID)
	}
	assert.Nil(t, api.preferredPositionVehicle(ctx, tripID, tomorrow.AddDate(0, 0, 1)))
}

func TestPreferredPositionVehicle_BlockAssignment(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createIsolatedTestApi(t)
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
		vehicle := api.preferredPositionVehicle(ctx, id, serviceDate)
		require.NotNil(t, vehicle)
		assert.Equal(t, "next", vehicle.ID.ID)
	}
	vehicle := api.preferredPositionVehicle(ctx, blocklessID, serviceDate)
	require.NotNil(t, vehicle)
	assert.Equal(t, "blockless", vehicle.ID.ID)
	assert.Nil(t, api.preferredPositionVehicle(ctx, "missing-trip", serviceDate))

	// A refresh must not change an assignment already shared by one request.
	api.GtfsManager.SetRealTimeVehiclesForTest([]gtfs.Vehicle{next, first})
	assert.Equal(t, "next", api.preferredPositionVehicle(ctx, nextID, serviceDate).ID.ID)
	assert.Equal(t, "first", api.preferredPositionVehicle(t.Context(), nextID, serviceDate).ID.ID)
}
