package restapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/nulls"
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

func TestTripForVehicleHandler_TripUpdateVehicle(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)
	agencyID := "dp-agency"
	combinedTripID := utils.FormCombinedID(agencyID, tripID)
	expired := now.Add(-time.Hour)

	namedUpdate := plannedVehicleTripUpdate(tripID, now)
	shortNamedUpdate := namedUpdate
	shortNamedUpdate.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "1519"}}
	anonymousUpdate := namedUpdate
	anonymousUpdate.Vehicle = nil
	expiredUpdate := plannedVehicleTripUpdate(tripID, expired)

	tests := []struct {
		name       string
		tripUpdate gtfs.Trip
		requestID  string
		wantTripID string
	}{
		{name: "raw ID the feed sent", tripUpdate: namedUpdate, requestID: plannedVehicleID, wantTripID: combinedTripID},
		{name: "vehicle ID with the agency", tripUpdate: shortNamedUpdate, requestID: utils.FormCombinedID(agencyID, "1519"), wantTripID: combinedTripID},
		{name: "block standing in as the vehicle", tripUpdate: anonymousUpdate, requestID: utils.FormCombinedID(agencyID, delayPropBlockID(t)), wantTripID: combinedTripID},
		{name: "unknown vehicle", tripUpdate: namedUpdate, requestID: utils.FormCombinedID(agencyID, "nobody")},
		{name: "another agency's block", tripUpdate: anonymousUpdate, requestID: utils.FormCombinedID("other", delayPropBlockID(t))},
		{name: "expired update", tripUpdate: expiredUpdate, requestID: plannedVehicleID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest gets its own rate limiter: the mock clock never refills one.
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddFullTripUpdate(tt.tripUpdate)

			resp, model := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", tt.requestID))

			if tt.wantTripID == "" {
				assert.Equal(t, http.StatusNotFound, resp.StatusCode)
				return
			}
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.wantTripID, model.Data.Entry.TripID)
		})
	}
}

// Feeds can send an anonymous update for every trip of a block, and Java reports
// the one the block is running now.
func TestTripForVehicleHandler_BlockVehicleReportsActiveTrip(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(10*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID, _, morningTripID, _ := setupDelayPropTestData(t, api, 1)
	laterTripID := addLaterBlockTrip(t, api, stopID, morningTripID)
	for _, tripID := range []string{morningTripID, laterTripID} {
		api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{ID: gtfs.TripID{ID: tripID}, Timestamp: &now})
	}
	vehicleID := utils.FormCombinedID("dp-agency", delayPropBlockID(t))

	resp, model := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", vehicleID))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, utils.FormCombinedID("dp-agency", laterTripID), model.Data.Entry.TripID)
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

// addLaterBlockTrip adds a trip running 10:00 to 11:00 to the block of the trip
// setupDelayPropTestData created, and returns its ID.
func addLaterBlockTrip(t *testing.T, api *RestAPI, stopID, tripID string) string {
	t.Helper()
	ctx := context.Background()
	queries := api.GtfsManager.GtfsDB.Queries
	trip, err := queries.GetTrip(ctx, tripID)
	require.NoError(t, err)
	laterTripID := tripID + "-later"
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: laterTripID, RouteID: trip.RouteID, ServiceID: trip.ServiceID, BlockID: trip.BlockID,
	})
	require.NoError(t, err)
	for i, startAt := range []time.Duration{10 * time.Hour, 11 * time.Hour} {
		_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: laterTripID, StopID: stopID, StopSequence: int64(i + 1),
			ArrivalTime: int64(startAt), DepartureTime: int64(startAt),
		})
		require.NoError(t, err)
	}
	return laterTripID
}

// A feed serving trip updates and positions from one URL makes go-gtfs build a
// vehicle from the update alone, with no timestamp when the update has none.
func TestBuildTripStatus_TripUpdateVehicleWithoutTimestamp(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8 * time.Hour)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	vehicle := &gtfs.Vehicle{
		ID:   &gtfs.VehicleID{ID: "bus"},
		Trip: &gtfs.Trip{ID: gtfs.TripID{ID: tripID}, IsEntityInMessage: true},
	}

	status, _, err := api.BuildTripStatus(context.Background(), "dp-agency", tripID, vehicle, serviceDate, now, nil)

	require.NoError(t, err)
	require.NotNil(t, status)
}

// Java hides the vehicle on a canceled arrival (ArrivalsAndDeparturesBeanServiceImpl
// .java:476) while the trip status still names it.
func TestArrivals_CanceledTripHidesVehicle(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	canceled := plannedVehicleTripUpdate(tripID, now)
	canceled.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
	api.GtfsManager.MockAddFullTripUpdate(canceled)
	combinedTripID := utils.FormCombinedID("dp-agency", tripID)

	_, plural := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
		"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120", combinedStopID))
	arrival := findArrivalForTrip(t, plural.Data.Entry.ArrivalsAndDepartures, combinedTripID)
	assert.Empty(t, arrival.VehicleID)
	assert.False(t, arrival.Predicted)
	require.NotNil(t, arrival.TripStatus)
	assert.Equal(t, "CANCELED", arrival.TripStatus.Status)

	_, singular := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
		combinedStopID, combinedTripID, serviceDate.UnixMilli()))
	assert.Empty(t, singular.Data.Entry.VehicleID)
	assert.False(t, singular.Data.Entry.Predicted)
}

// Java builds one record per vehicle whose time is the block's last timestamp
// (GtfsRealtimeTripLibrary.java:948-950), so the block's updates expire together.
func TestBlockTripUpdates_ExpireTogether(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8 * time.Hour)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	stopID, _, firstTripID, _ := setupDelayPropTestData(t, api, 1)
	laterTripID := addLaterBlockTrip(t, api, stopID, firstTripID)
	fresh := now.Add(-time.Minute)
	stale := now.Add(-10 * time.Minute)

	tests := []struct {
		name        string
		first, last time.Time
		wantRecord  bool
	}{
		{name: "stale first update with a fresh last one keeps the record", first: stale, last: fresh, wantRecord: true},
		{name: "fresh first update with a stale last one loses the record", first: fresh, last: stale},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddVehicleWithOptions("bus", firstTripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			for tripID, timestamp := range map[string]time.Time{firstTripID: tt.first, laterTripID: tt.last} {
				api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
					ID: gtfs.TripID{ID: tripID}, Timestamp: &timestamp,
					Vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
				})
			}

			tripUpdates := api.collectBlockTripUpdates([]string{firstTripID, laterTripID})
			vehicle := api.vehicleForTrip(context.Background(), firstTripID, serviceDate)

			if tt.wantRecord {
				assert.Len(t, tripUpdates, 2)
			} else {
				assert.Empty(t, tripUpdates)
			}
			assert.Equal(t, tt.wantRecord, vehicle != nil)
		})
	}
}

// A vehicle ID an arrival reports must work when a client sends it back.
func TestReportedTripUpdateVehicleIDRoundTrips(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)
	combinedTripID := utils.FormCombinedID("dp-agency", tripID)

	tests := []struct {
		name          string
		feedVehicleID string
		requestID     string
		wantFound     bool
	}{
		{name: "raw ID", feedVehicleID: plannedVehicleID, requestID: plannedVehicleID, wantFound: true},
		{name: "ID with the agency", feedVehicleID: "1519", requestID: "dp-agency_1519", wantFound: true},
		{name: "another vehicle", feedVehicleID: "1519", requestID: "dp-agency_1520"},
		{name: "same ID under another agency", feedVehicleID: "1519", requestID: "other_1519"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			tripUpdate := plannedVehicleTripUpdate(tripID, now)
			tripUpdate.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: tt.feedVehicleID}}
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

			details, _ := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf(
				"/api/where/trip-details/%s.json?key=TEST&serviceDate=%d&vehicleId=%s", combinedTripID, serviceDate.UnixMilli(), tt.requestID))
			_, arrival := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
				"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1&vehicleId=%s",
				combinedStopID, combinedTripID, serviceDate.UnixMilli(), tt.requestID))

			if !tt.wantFound {
				assert.Equal(t, http.StatusNotFound, details.StatusCode)
				assert.Empty(t, arrival.Data.Entry.VehicleID)
				return
			}
			assert.Equal(t, http.StatusOK, details.StatusCode)
			assert.Equal(t, tt.requestID, arrival.Data.Entry.VehicleID)
		})
	}
}

func TestTripForVehicleHandler_NamedVehicleReportsActiveTrip(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(10*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID, _, morningTripID, _ := setupDelayPropTestData(t, api, 1)
	laterTripID := addLaterBlockTrip(t, api, stopID, morningTripID)
	for _, tripID := range []string{morningTripID, laterTripID} {
		api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now))
	}

	resp, model := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", plannedVehicleID))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, utils.FormCombinedID("dp-agency", laterTripID), model.Data.Entry.TripID)
}

// A trip past midnight belongs to the previous service date, and a vehicle known
// only from its trip update is still that trip's vehicle then.
func TestTripForVehicleHandler_TripUpdateVehicleAfterMidnight(t *testing.T) {
	previousServiceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := previousServiceDate.Add(25*time.Hour + 10*time.Minute)

	tests := []struct {
		name         string
		hasStartDate bool
	}{
		{name: "update with a start date", hasStartDate: true},
		{name: "update without a start date"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			ctx := context.Background()
			stopID, _, baseTripID, _ := setupDelayPropTestData(t, api, 1)
			queries := api.GtfsManager.GtfsDB.Queries
			baseTrip, err := queries.GetTrip(ctx, baseTripID)
			require.NoError(t, err)
			lateTripID := baseTripID + "-late"
			_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
				ID: lateTripID, RouteID: baseTrip.RouteID, ServiceID: baseTrip.ServiceID, BlockID: nulls.String(lateTripID),
			})
			require.NoError(t, err)
			for i, startAt := range []time.Duration{25 * time.Hour, 26 * time.Hour} {
				_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
					TripID: lateTripID, StopID: stopID, StopSequence: int64(i + 1),
					ArrivalTime: int64(startAt), DepartureTime: int64(startAt),
				})
				require.NoError(t, err)
			}
			tripUpdate := plannedVehicleTripUpdate(lateTripID, now)
			tripUpdate.ID.HasStartDate = tt.hasStartDate
			tripUpdate.ID.StartDate = previousServiceDate
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

			resp, model := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", plannedVehicleID))

			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, utils.FormCombinedID("dp-agency", lateTripID), model.Data.Entry.TripID)
			require.NotNil(t, model.Data.Entry.Status)
			assert.Equal(t, plannedVehicleID, model.Data.Entry.Status.VehicleID)
			assert.Equal(t, "in_progress", model.Data.Entry.Status.Phase)
		})
	}
}

// queryCountingDB counts the queries that reach the database.
type queryCountingDB struct {
	gtfsdb.DBTX
	queries int
}

func (db *queryCountingDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	db.queries++
	return db.DBTX.QueryContext(ctx, query, args...)
}

func (db *queryCountingDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	db.queries++
	return db.DBTX.QueryRowContext(ctx, query, args...)
}

func countQueries(t *testing.T, api *RestAPI) *queryCountingDB {
	t.Helper()
	originalQueries := api.GtfsManager.GtfsDB.Queries
	t.Cleanup(func() { api.GtfsManager.GtfsDB.Queries = originalQueries })
	counter := &queryCountingDB{DBTX: api.GtfsManager.GtfsDB.DB}
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(counter)
	return counter
}

func TestTripUpdateVehicleForTrip_NoQueriesWithoutTripUpdates(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(serviceDate.Add(8*time.Hour)))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	counter := countQueries(t, api)

	assert.Nil(t, api.tripUpdateVehicleForTrip(context.Background(), tripID, serviceDate))
	assert.Zero(t, counter.queries)
}

// Any caller can ask for an unknown vehicle, so each one must not cost a query
// per trip update in the feed.
func TestTripUpdateVehicleTrip_UnknownVehicleSkipsTripLookups(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8 * time.Hour)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now))
	counter := countQueries(t, api)

	_, _, found := api.tripUpdateVehicleTrip(context.Background(), "dp-agency", "nobody")

	assert.False(t, found)
	assert.Equal(t, 1, counter.queries, "only the block lookup should run")
}

func ptrTime(t time.Time) *time.Time { return &t }

// A block's trip updates form one record, timed by the last one
// (GtfsRealtimeTripLibrary.java:948-950), even when the vehicle comes from the first.
func TestArrivals_TripUpdateVehicleUsesBlockRecordTime(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	old := now.Add(-20 * time.Minute)

	tests := []struct {
		name         string
		relationship gtfsrt.TripDescriptor_ScheduleRelationship
		wantStatus   string
		wantPhase    string
		wantVehicle  string
		wantPredict  bool
	}{
		{name: "scheduled", wantStatus: "SCHEDULED", wantPhase: "in_progress", wantVehicle: plannedVehicleID, wantPredict: true},
		{name: "first update canceled", relationship: gtfsrt.TripDescriptor_CANCELED, wantStatus: "CANCELED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			stopID := addDownstreamStop(t, api, tripID)
			laterTripID := addLaterBlockTrip(t, api, stopID, tripID)
			firstUpdate := plannedVehicleTripUpdate(tripID, old)
			firstUpdate.ID.ScheduleRelationship = tt.relationship
			api.GtfsManager.MockAddFullTripUpdate(firstUpdate)
			api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(laterTripID, now))
			endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
				utils.FormCombinedID("dp-agency", stopID))

			_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

			arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
			require.NotNil(t, arrival.TripStatus)
			assert.Equal(t, tt.wantStatus, arrival.TripStatus.Status)
			assert.Equal(t, tt.wantPhase, arrival.TripStatus.Phase)
			assert.Equal(t, tt.wantVehicle, arrival.VehicleID)
			assert.Equal(t, tt.wantPredict, arrival.Predicted)
			assert.Equal(t, now.UnixMilli(), arrival.TripStatus.LastUpdateTime.UnixMilli())
		})
	}
}

// Java drops a record over an hour off schedule (GtfsRealtimeSource.java:728) and
// has no location once the record's schedule time is past the block's last stop
// (AbstractBlockLocationServiceImpl.java:274), unless the trip is canceled. The
// fixture's block ends at 09:05.
func TestTripUpdateVehicleForTrip_DropsRecordsJavaDiscards(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		at           time.Duration
		delay        time.Duration
		relationship gtfsrt.TripDescriptor_ScheduleRelationship
		wantVehicle  bool
	}{
		{name: "on schedule during the block", at: 8*time.Hour + 30*time.Minute, wantVehicle: true},
		{name: "over an hour late", at: 8*time.Hour + 30*time.Minute, delay: 2 * time.Hour},
		{name: "over an hour early", at: 8*time.Hour + 30*time.Minute, delay: -2 * time.Hour},
		{name: "added trip over an hour late", at: 8*time.Hour + 30*time.Minute, delay: 2 * time.Hour, relationship: gtfsrt.TripDescriptor_ADDED, wantVehicle: true},
		{name: "past the block's end", at: 9*time.Hour + 40*time.Minute},
		{name: "past the end but running late enough", at: 9*time.Hour + 40*time.Minute, delay: 40 * time.Minute, wantVehicle: true},
		{name: "canceled past the block's end", at: 9*time.Hour + 40*time.Minute, relationship: gtfsrt.TripDescriptor_CANCELED, wantVehicle: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := serviceDate.Add(tt.at)
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, tripID)
			tripUpdate := plannedVehicleTripUpdate(tripID, now)
			tripUpdate.ID.ScheduleRelationship = tt.relationship
			tripUpdate.Delay = &tt.delay
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

			vehicle := api.tripUpdateVehicleForTrip(context.Background(), tripID, serviceDate)

			assert.Equal(t, tt.wantVehicle, vehicle != nil)
		})
	}
}

// Java's record takes its status from the trip update, and a vehicle position only
// adds a location (GtfsRealtimeTripLibrary.java:325, :1101-1102).
func TestArrivals_CanceledTripUpdateHidesRealBus(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)

	tests := []struct {
		name          string
		updateVehicle string
		wantStatus    string
		wantHidden    bool
	}{
		{name: "update names the bus", updateVehicle: "bus", wantStatus: "CANCELED", wantHidden: true},
		{name: "update names no vehicle", wantStatus: "CANCELED", wantHidden: true},
		{name: "update names another vehicle", updateVehicle: "other", wantStatus: "SCHEDULED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			stopID := addDownstreamStop(t, api, tripID)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			tripUpdate := gtfs.Trip{
				ID:        gtfs.TripID{ID: tripID, ScheduleRelationship: gtfsrt.TripDescriptor_CANCELED},
				Timestamp: &now,
			}
			if tt.updateVehicle != "" {
				tripUpdate.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: tt.updateVehicle}}
			}
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)
			endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
				utils.FormCombinedID("dp-agency", stopID))

			_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

			arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
			require.NotNil(t, arrival.TripStatus)
			assert.Equal(t, tt.wantStatus, arrival.TripStatus.Status)
			assert.Equal(t, tt.wantHidden, arrival.VehicleID == "")
			if tt.wantHidden {
				assert.False(t, arrival.Predicted)
				assert.Zero(t, arrival.PredictedArrivalTime)
			}

			original, err := api.GtfsManager.GetVehicleByID("bus")
			require.NoError(t, err)
			assert.NotEqual(t, gtfsrt.TripDescriptor_CANCELED, original.Trip.ID.ScheduleRelationship, "the shared vehicle stays unchanged")
		})
	}
}

func TestTripDetailsHandler_ReportedVehicleAfterMidnight(t *testing.T) {
	previousServiceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := previousServiceDate.Add(25*time.Hour + 10*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()
	stopID, _, baseTripID, _ := setupDelayPropTestData(t, api, 1)
	queries := api.GtfsManager.GtfsDB.Queries
	baseTrip, err := queries.GetTrip(ctx, baseTripID)
	require.NoError(t, err)
	lateTripID := baseTripID + "-late"
	_, err = queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: lateTripID, RouteID: baseTrip.RouteID, ServiceID: baseTrip.ServiceID, BlockID: nulls.String(lateTripID),
	})
	require.NoError(t, err)
	for i, startAt := range []time.Duration{25 * time.Hour, 26 * time.Hour} {
		_, err = queries.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: lateTripID, StopID: stopID, StopSequence: int64(i + 1),
			ArrivalTime: int64(startAt), DepartureTime: int64(startAt),
		})
		require.NoError(t, err)
	}
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(lateTripID, now))

	resp, model := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf("/api/where/trip-details/%s.json?key=TEST&vehicleId=%s",
		utils.FormCombinedID("dp-agency", lateTripID), plannedVehicleID))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, previousServiceDate.UnixMilli(), model.Data.Entry.ServiceDate.UnixMilli())
}
