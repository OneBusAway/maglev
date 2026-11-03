package restapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

const plannedVehicleID = "block_9_schedBasedVehicle"

func plannedVehicleTripUpdate(tripID string, timestamp time.Time) gtfs.Trip {
	return gtfs.Trip{
		ID:        gtfs.TripID{ID: tripID},
		Timestamp: &timestamp,
		Vehicle:   &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: plannedVehicleID}},
	}
}

func TestTripUpdateVehicleForTrip(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7 * time.Hour) // setupDelayPropTestData's block starts at 08:00
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)

	withStartDate := func(tripUpdate gtfs.Trip, startDate time.Time) gtfs.Trip {
		tripUpdate.ID.HasStartDate = true
		tripUpdate.ID.StartDate = startDate
		return tripUpdate
	}
	withoutVehicle := plannedVehicleTripUpdate(tripID, now)
	withoutVehicle.Vehicle = nil

	tests := []struct {
		name          string
		tripUpdate    gtfs.Trip
		vehicleInFeed bool
		wantVehicle   bool
	}{
		{name: "fresh update within an hour of the block", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(time.Minute)), wantVehicle: true},
		{name: "update for this service date", tripUpdate: withStartDate(plannedVehicleTripUpdate(tripID, now.Add(time.Minute)), serviceDate), wantVehicle: true},
		{name: "update more than an hour before the block", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(-time.Minute))},
		{name: "expired update", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(-time.Hour))},
		{name: "update without a vehicle", tripUpdate: withoutVehicle},
		{name: "update for another service date", tripUpdate: withStartDate(plannedVehicleTripUpdate(tripID, now.Add(time.Minute)), serviceDate.AddDate(0, 0, 1))},
		{name: "vehicle that reports a position", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(time.Minute)), vehicleInFeed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddFullTripUpdate(tt.tripUpdate)
			if tt.vehicleInFeed {
				api.GtfsManager.MockAddVehicle(plannedVehicleID, "another-trip", "another-route")
			}

			vehicle := api.tripUpdateVehicleForTrip(context.Background(), tripID, serviceDate)

			if !tt.wantVehicle {
				assert.Nil(t, vehicle)
				return
			}
			require.NotNil(t, vehicle)
			assert.Equal(t, plannedVehicleID, vehicle.ID.ID)
			assert.Equal(t, tt.tripUpdate.Timestamp, vehicle.Timestamp)
			assert.True(t, isTripUpdateVehicle(vehicle))
		})
	}
}

// A block that has not started yet reports the vehicle waiting at its first
// stop, frozen at the update's timestamp: live Tampa's block_*_schedBasedVehicle.
func TestPluralArrivals_TripUpdateVehicleBeforeBlockStart(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	firstStopID, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	downstreamStopID := addDownstreamStop(t, api, tripID)
	recordTime := now.Add(-time.Minute)
	tripUpdate := plannedVehicleTripUpdate(tripID, recordTime)
	tripUpdate.StopTimeUpdates = []gtfs.StopTimeUpdate{{StopID: &downstreamStopID, Arrival: &gtfs.StopTimeEvent{Time: ptrTime(serviceDate.Add(9 * time.Hour))}}}
	api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

	endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
		utils.FormCombinedID("dp-agency", downstreamStopID))
	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

	arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
	assert.Equal(t, plannedVehicleID, arrival.VehicleID)
	assert.Equal(t, recordTime.UnixMilli(), arrival.LastUpdateTime.UnixMilli())
	assert.Equal(t, 1, arrival.NumberOfStopsAway, "the vehicle waits at the block's first stop, one stop away")

	status := arrival.TripStatus
	require.NotNil(t, status)
	assert.Equal(t, plannedVehicleID, status.VehicleID)
	assert.Equal(t, "in_progress", status.Phase)
	assert.Equal(t, "SCHEDULED", status.Status)
	assert.True(t, status.Predicted)
	assert.Equal(t, utils.FormCombinedID("dp-agency", tripID), status.ActiveTripID)
	assert.Zero(t, status.ScheduledDistanceAlongTrip)
	assert.Equal(t, utils.FormCombinedID("dp-agency", firstStopID), status.ClosestStop)
	assert.Equal(t, utils.FormCombinedID("dp-agency", firstStopID), status.NextStop)
	assert.Equal(t, 31*60, status.ClosestStopTimeOffset, "seconds from the update's timestamp to the block's start")
	assert.Equal(t, 31*60, status.NextStopTimeOffset)
}

func TestSingularArrival_TripUpdateVehicle(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now.Add(-time.Minute)))

	endpoint := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
		combinedStopID, utils.FormCombinedID("dp-agency", tripID), serviceDate.UnixMilli())
	_, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, endpoint)

	assert.Equal(t, plannedVehicleID, model.Data.Entry.VehicleID)
	require.NotNil(t, model.Data.Entry.TripStatus)
	assert.Equal(t, plannedVehicleID, model.Data.Entry.TripStatus.VehicleID)
}

func TestTripUpdateVehicleResponseID(t *testing.T) {
	tests := []struct {
		name      string
		vehicleID string
		want      string
	}{
		{name: "an ID Java can split into agency and ID stays as sent", vehicleID: plannedVehicleID, want: plannedVehicleID},
		{name: "an ID without a separator gets the agency", vehicleID: "1519", want: "1_1519"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: tt.vehicleID}, Trip: &gtfs.Trip{IsEntityInMessage: true}}
			assert.Equal(t, tt.want, responseVehicleID("1", vehicle))
		})
	}
}

func TestResponseVehicleID_PositionVehicleAlwaysGetsAgency(t *testing.T) {
	vehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: plannedVehicleID}, IsEntityInMessage: true}
	assert.Equal(t, "1_"+plannedVehicleID, responseVehicleID("1", vehicle))
}

func ptrTime(t time.Time) *time.Time { return &t }
