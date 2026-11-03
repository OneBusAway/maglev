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
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
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
	withoutVehicle := plannedVehicleTripUpdate(tripID, now.Add(time.Minute))
	withoutVehicle.Vehicle = nil

	tests := []struct {
		name          string
		tripUpdate    gtfs.Trip
		vehicleInFeed bool
		wantVehicle   bool
		wantVehicleID string
	}{
		{name: "fresh update within an hour of the block", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(time.Minute)), wantVehicle: true},
		{name: "update for this service date", tripUpdate: withStartDate(plannedVehicleTripUpdate(tripID, now.Add(time.Minute)), serviceDate), wantVehicle: true},
		{name: "update more than an hour before the block", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(-time.Minute))},
		{name: "expired update", tripUpdate: plannedVehicleTripUpdate(tripID, now.Add(-time.Hour))},
		{name: "update without a vehicle uses the block as the vehicle", tripUpdate: withoutVehicle, wantVehicle: true, wantVehicleID: delayPropBlockID(t)},
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
			wantVehicleID := tt.wantVehicleID
			if wantVehicleID == "" {
				wantVehicleID = plannedVehicleID
			}
			assert.Equal(t, wantVehicleID, vehicle.ID.ID)
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

// Java keeps a block location for a schedule time before the block starts: at
// the block's first stop, however long before. Only past the block's end is
// there none. Live Tampa reported these for blocks nine hours away.
func TestPluralArrivals_BeforeBlockStartMeasuresFromFirstStop(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	const vehicleID = "bus-before-block-start"

	tests := []struct {
		name        string
		addRealtime func(api *RestAPI, tripID string)
		wantPhase   string
		wantVehicle string
	}{
		{
			name:        "no vehicle",
			addRealtime: func(*RestAPI, string) {},
			wantPhase:   "scheduled",
		},
		{
			name: "vehicle reporting a position",
			addRealtime: func(api *RestAPI, tripID string) {
				now := serviceDate.Add(7 * time.Hour)
				api.GtfsManager.MockAddVehicleWithOptions(vehicleID, tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			},
			wantPhase:   "in_progress",
			wantVehicle: utils.FormCombinedID("dp-agency", vehicleID),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(serviceDate.Add(7*time.Hour)))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)

			firstStopID, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			downstreamStopID := addDownstreamStop(t, api, tripID)
			tt.addRealtime(api, tripID)

			endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=180",
				utils.FormCombinedID("dp-agency", downstreamStopID))
			_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

			arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
			assert.Equal(t, 1, arrival.NumberOfStopsAway, "the block's first stop is one stop before this one")
			require.NotNil(t, arrival.TripStatus)
			assert.Equal(t, tt.wantPhase, arrival.TripStatus.Phase)
			assert.Equal(t, tt.wantVehicle, arrival.TripStatus.VehicleID)
			assert.Equal(t, utils.FormCombinedID("dp-agency", tripID), arrival.TripStatus.ActiveTripID)
			if tt.wantVehicle == "" {
				assert.Equal(t, utils.FormCombinedID("dp-agency", firstStopID), arrival.TripStatus.NextStop)
				assert.Equal(t, 60*60, arrival.TripStatus.NextStopTimeOffset)
			}
		})
	}
}

func TestPluralArrivals_AfterBlockEndReportsNoDistance(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(serviceDate.Add(9*time.Hour+30*time.Minute)))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	downstreamStopID := addDownstreamStop(t, api, tripID)

	endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=60&minutesAfter=0",
		utils.FormCombinedID("dp-agency", downstreamStopID))
	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

	arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
	assert.Zero(t, arrival.NumberOfStopsAway)
	assert.Zero(t, arrival.DistanceFromStop)
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
			named := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: tt.vehicleID}}
			vehicle := &gtfs.Vehicle{ID: named.ID, Trip: &gtfs.Trip{IsEntityInMessage: true, Vehicle: named}}
			assert.Equal(t, tt.want, responseVehicleID("1", vehicle))
		})
	}
}

// Java qualifies a block standing in as the vehicle with the agency, even when
// the block's ID contains the separator.
func TestResponseVehicleID_BlockAsVehicleAlwaysGetsAgency(t *testing.T) {
	vehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "block_1"}, Trip: &gtfs.Trip{IsEntityInMessage: true}}
	assert.Equal(t, "1_block_1", responseVehicleID("1", vehicle))
}

func TestResponseVehicleID_PositionVehicleAlwaysGetsAgency(t *testing.T) {
	vehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: plannedVehicleID}, IsEntityInMessage: true}
	assert.Equal(t, "1_"+plannedVehicleID, responseVehicleID("1", vehicle))
}

// A trip update naming no vehicle still builds a record in Java, with the block's
// ID as the vehicle, which is how live Tampa reports 1_501 on block 501.
func TestPluralArrivals_TripUpdateWithoutVehicleUsesBlock(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	downstreamStopID := addDownstreamStop(t, api, tripID)
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
		ID:              gtfs.TripID{ID: tripID},
		Timestamp:       &now,
		StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &downstreamStopID, Arrival: &gtfs.StopTimeEvent{Time: ptrTime(serviceDate.Add(9 * time.Hour))}}},
	})
	endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
		utils.FormCombinedID("dp-agency", downstreamStopID))

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

	arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
	wantVehicleID := utils.FormCombinedID("dp-agency", delayPropBlockID(t))
	assert.Equal(t, wantVehicleID, arrival.VehicleID)
	assert.True(t, arrival.Predicted)
	require.NotNil(t, arrival.TripStatus)
	assert.Equal(t, wantVehicleID, arrival.TripStatus.VehicleID)
	assert.Equal(t, "in_progress", arrival.TripStatus.Phase)
	assert.Equal(t, "SCHEDULED", arrival.TripStatus.Status)
}

// Java keeps a vehicle where its record placed it until a newer record arrives,
// so a real bus's stops and distance do not move between requests. The vehicle's
// position does not change this: location interpolation is off by default.
func TestPluralArrivals_RealBusStaysAtRecordPosition(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	recordTime := serviceDate.Add(8*time.Hour + 30*time.Minute)
	mockClock := clock.NewMockClock(recordTime)
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	downstreamStopID := addDownstreamStop(t, api, tripID)
	latitude, longitude := float32(47.0), float32(-122.0)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{
		Position:  &gtfs.Position{Latitude: &latitude, Longitude: &longitude},
		Timestamp: &recordTime,
	})
	predictedTenMinutesLate := serviceDate.Add(9*time.Hour + 10*time.Minute)
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
		ID:              gtfs.TripID{ID: tripID},
		Timestamp:       &recordTime,
		Vehicle:         &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
		StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &downstreamStopID, Arrival: &gtfs.StopTimeEvent{Time: &predictedTenMinutesLate}}},
	})
	endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
		utils.FormCombinedID("dp-agency", downstreamStopID))

	for _, minutesAfterRecord := range []time.Duration{0, 3} {
		mockClock.Set(recordTime.Add(minutesAfterRecord * time.Minute))
		_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

		status := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID)).TripStatus
		require.NotNil(t, status)
		assert.Equal(t, 600, status.ScheduleDeviation)
		assert.Equal(t, utils.FormCombinedID("dp-agency", downstreamStopID), status.NextStop)
		assert.Equal(t, 40*60, status.NextStopTimeOffset, "09:00 scheduled, from 08:20 on the schedule")
	}
}

// Java has no record for a vehicle once its trip update's timestamp is over five
// minutes old, so the arrival is schedule-only, even while Maglev still retains
// the vehicle's last position.
func TestVehicleForTrip_DropsVehicleWithExpiredRecord(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8 * time.Hour)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)

	tests := []struct {
		name        string
		updateAge   *time.Duration
		wantVehicle bool
	}{
		{name: "no trip update keeps the position-only vehicle", wantVehicle: true},
		{name: "fresh trip update keeps the vehicle", updateAge: ptrDuration(5 * time.Minute), wantVehicle: true},
		{name: "expired trip update drops the vehicle", updateAge: ptrDuration(5*time.Minute + time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			if tt.updateAge != nil {
				timestamp := now.Add(-*tt.updateAge)
				api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
					ID:        gtfs.TripID{ID: tripID},
					Timestamp: &timestamp,
					Vehicle:   &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
				})
			}

			vehicle := api.vehicleForTrip(context.Background(), tripID, serviceDate)

			assert.Equal(t, tt.wantVehicle, vehicle != nil)
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
