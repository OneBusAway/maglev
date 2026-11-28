package restapi

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
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
				api.GtfsManager.MockAddVehicle(plannedVehicleID, "another-trip", "dp-route")
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

// A feed serving trip updates at its vehicle positions URL makes go-gtfs add a
// position-less vehicle for each trip update naming one. That vehicle is known
// only from the trip update, so it must pass the same checks as one built here.
func TestVehicleForTrip_LibraryTripUpdateVehicle(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7 * time.Hour) // setupDelayPropTestData's block starts at 08:00
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)

	tests := []struct {
		name        string
		recordTime  time.Time
		wantVehicle bool
	}{
		{name: "record within an hour of the block", recordTime: now.Add(time.Minute), wantVehicle: true},
		{name: "record more than an hour before the block", recordTime: now.Add(-time.Minute)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			tripUpdate := plannedVehicleTripUpdate(tripID, tt.recordTime)
			tripUpdate.IsEntityInMessage = true
			libraryVehicle := gtfs.Vehicle{ID: tripUpdate.Vehicle.ID, Trip: &tripUpdate}
			api.GtfsManager.MockSetAgencyVehicles("_test", "dp-agency", []gtfs.Vehicle{libraryVehicle})
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

			vehicle := api.vehicleForTrip(context.Background(), tripID, serviceDate)

			if !tt.wantVehicle {
				assert.Nil(t, vehicle)
				return
			}
			require.NotNil(t, vehicle)
			require.NotNil(t, vehicle.Timestamp, "the record's time")
			assert.Equal(t, tt.recordTime, *vehicle.Timestamp)
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

// The trip status resolves the record once; the snapshot must place the block
// by that record instead of rereading trip updates, which may have expired since.
func TestStatusBlockSnapshot_UsesSuppliedRecord(t *testing.T) {
	serviceDate := time.Date(2026, time.March, 7, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(11 * time.Hour)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	firstStopID, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	addDownstreamStop(t, api, tripID)
	record := vehicleRecord{time: serviceDate.Add(8*time.Hour - 5*time.Minute), present: true}

	status := &models.TripStatus{}
	snap := api.statusBlockSnapshot(t.Context(), status, tripStatusInput{
		agencyID: "dp-agency", tripID: tripID, dbTripID: tripID,
		serviceDate: serviceDate, currentTime: now,
	}, record, 0)

	require.NotNil(t, snap)
	assert.Equal(t, utils.FormCombinedID("dp-agency", firstStopID), status.NextStop)
	assert.Equal(t, 5*60, status.NextStopTimeOffset)
}

func TestStatusBlockSnapshot_BeforeStartUsesWallClock(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	tests := []struct {
		name      string
		month     time.Month
		day       int
		firstStop time.Duration
	}{
		{name: "spring forward", month: time.March, day: 8, firstStop: 90 * time.Minute},
		{name: "fall back", month: time.November, day: 1, firstStop: 90 * time.Minute},
		{name: "ordinary day", month: time.March, day: 7, firstStop: 90 * time.Minute},
		{name: "overnight on spring forward date", month: time.March, day: 8, firstStop: 25*time.Hour + 30*time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceDate := time.Date(2026, tt.month, tt.day, 0, 0, 0, 0, location)
			now := time.Date(2026, tt.month, tt.day, 0, 0, int((tt.firstStop-30*time.Minute)/time.Second), 0, location)
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			firstStopID, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, tripID)
			shift := int64(tt.firstStop - 8*time.Hour)
			_, err := api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(),
				"UPDATE stop_times SET arrival_time = arrival_time + ?, departure_time = departure_time + ? WHERE trip_id = ?",
				shift, shift, tripID)
			require.NoError(t, err)

			status := &models.TripStatus{}
			snap := api.statusBlockSnapshot(t.Context(), status, tripStatusInput{
				agencyID: "dp-agency", tripID: tripID, dbTripID: tripID,
				serviceDate: serviceDate, currentTime: now,
			}, vehicleRecord{}, 0)

			require.NotNil(t, snap)
			assert.True(t, snap.InRange, "held snapshot must be at the first stop, not before it")
			assert.Equal(t, tripID, snap.ActiveTripID)
			assert.Zero(t, snap.DistanceAlongBlock)
			assert.Zero(t, snap.NextStopIndex, "held snapshot must not advance past the first stop")
			assert.Equal(t, utils.FormCombinedID("dp-agency", firstStopID), status.NextStop)
			assert.Equal(t, status.NextStop, status.ClosestStop)
			assert.Equal(t, 30*60, status.NextStopTimeOffset)
			assert.Equal(t, status.NextStopTimeOffset, status.ClosestStopTimeOffset)
			_, stopsAway, ok := snap.metricsForStop(tripID, 5)
			assert.True(t, ok, "pre-start hold must support downstream arrival metrics")
			assert.Equal(t, 1, stopsAway)
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
	addDownstreamStop(t, setupAPI, tripID) // runs the block to 09:05, past now
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
	positionTime := recordTime.Add(-20 * time.Second)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{
		Position:  &gtfs.Position{Latitude: &latitude, Longitude: &longitude},
		Timestamp: &positionTime,
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

		arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
		assert.Equal(t, recordTime.UnixMilli(), arrival.LastUpdateTime.UnixMilli())
		status := arrival.TripStatus
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

// Canceled arrivals are hidden, as with Java's default hideCanceledTrips
// (ArrivalsAndDeparturesBeanServiceImpl.java:267; wiki SD-03).
func TestArrivals_CanceledTripIsHidden(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute)

	tests := []struct {
		name   string
		cancel func(api *RestAPI, tripID string)
	}{
		{name: "trip update cancels the trip", cancel: func(api *RestAPI, tripID string) {
			canceled := plannedVehicleTripUpdate(tripID, now)
			canceled.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
			api.GtfsManager.MockAddFullTripUpdate(canceled)
		}},
		{name: "vehicle position reports the trip canceled", cancel: func(api *RestAPI, tripID string) {
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{
				Timestamp:            &now,
				ScheduleRelationship: gtfsrt.TripDescriptor_CANCELED,
			})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
			tt.cancel(api, tripID)
			combinedTripID := utils.FormCombinedID("dp-agency", tripID)

			_, plural := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
				"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120", combinedStopID))
			assertNoArrivalForTrip(t, plural.Data.Entry.ArrivalsAndDepartures, combinedTripID)

			_, nearby := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
				"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=47.0&lon=-122.0&radius=100&minutesBefore=0&minutesAfter=120")
			assertNoArrivalForTrip(t, nearby.Data.Entry.ArrivalsAndDepartures, combinedTripID)

			singularResp, _ := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
				"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
				combinedStopID, combinedTripID, serviceDate.UnixMilli()))
			assert.Equal(t, http.StatusNotFound, singularResp.StatusCode)
		})
	}
}

func assertNoArrivalForTrip(t *testing.T, arrivals []models.ArrivalAndDeparture, tripID string) {
	t.Helper()
	for _, arrival := range arrivals {
		assert.NotEqual(t, tripID, arrival.TripID, "a canceled trip's arrival is hidden")
	}
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

func TestExplicitVehicleIDUsesRecordChecks(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, stopID, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)
	tests := []struct {
		name        string
		requestedID string
		recordAge   time.Duration
		canceled    bool
		wantCode    int
		// wantArrivalCode is the singular arrival's status code when it differs from wantCode.
		wantArrivalCode int
	}{
		{name: "fresh", requestedID: "dp-agency_bus", wantCode: http.StatusOK},
		{name: "expired", requestedID: "dp-agency_bus", recordAge: 20 * time.Minute, wantCode: http.StatusNotFound},
		{name: "canceled", requestedID: "dp-agency_bus", canceled: true, wantCode: http.StatusOK, wantArrivalCode: http.StatusNotFound},
		{name: "wrong vehicle", requestedID: "dp-agency_other", wantCode: http.StatusNotFound},
		{name: "wrong agency", requestedID: "other_bus", wantCode: http.StatusNotFound},
		{name: "canceled wrong vehicle", requestedID: "dp-agency_other", canceled: true, wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			update := plannedVehicleTripUpdate(tripID, now.Add(-tt.recordAge))
			update.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}}
			delay := time.Minute
			update.Delay = &delay
			if tt.canceled {
				update.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
			}
			api.GtfsManager.MockAddFullTripUpdate(update)
			combinedTripID := utils.FormCombinedID("dp-agency", tripID)
			query := fmt.Sprintf("key=TEST&serviceDate=%d&vehicleId=%s", serviceDate.UnixMilli(), tt.requestedID)

			detailsResp, details := callAPIHandler[TripDetailsResponse](t, api,
				fmt.Sprintf("/api/where/trip-details/%s.json?%s", combinedTripID, query))
			arrivalResp, arrival := callAPIHandler[ArrivalAndDepartureResponse](t, api,
				fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?%s&tripId=%s&stopSequence=1", stopID, query, combinedTripID))

			wantArrivalCode := tt.wantArrivalCode
			if wantArrivalCode == 0 {
				wantArrivalCode = tt.wantCode
			}
			require.Equal(t, tt.wantCode, detailsResp.StatusCode)
			require.Equal(t, wantArrivalCode, arrivalResp.StatusCode)
			if tt.wantCode != http.StatusOK {
				return
			}
			require.NotNil(t, details.Data.Entry.Status)
			if tt.canceled {
				assert.Equal(t, "CANCELED", details.Data.Entry.Status.Status)
				assert.Empty(t, details.Data.Entry.Status.Phase)
				return
			}
			require.NotNil(t, arrival.Data.Entry.TripStatus)
			assert.Equal(t, details.Data.Entry.Status.Status, arrival.Data.Entry.TripStatus.Status)
			assert.Equal(t, tt.requestedID, details.Data.Entry.Status.VehicleID)
			assert.Equal(t, tt.requestedID, arrival.Data.Entry.VehicleID)
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

type recordExpiryBoundaryClock struct {
	now        time.Time
	freshReads int64
	reads      atomic.Int64
}

// Now crosses the record's expiry boundary after the configured reads.
func (c *recordExpiryBoundaryClock) Now() time.Time {
	if c.reads.Add(1) > c.freshReads {
		return c.now.Add(time.Nanosecond)
	}
	return c.now
}

func TestActiveTripOfTripUpdateVehicle_RecordExpiry(t *testing.T) {
	now := time.Date(2010, 1, 1, 10, 1, 0, 0, time.UTC)
	tests := []struct {
		name         string
		recordAge    time.Duration
		anonymous    bool
		boundary     bool
		relationship gtfs.TripScheduleRelationship
		wantFound    bool
		wantLater    bool
	}{
		{name: "fresh named record keeps its delay", recordAge: 2 * time.Minute, wantFound: true},
		{name: "fresh anonymous record keeps its delay", recordAge: 2 * time.Minute, anonymous: true, wantFound: true},
		{name: "already expired record is rejected", recordAge: 6 * time.Minute},
		{name: "named record expires during lookup", recordAge: 5 * time.Minute, boundary: true, wantFound: true, wantLater: true},
		{name: "anonymous record expires during lookup", recordAge: 5 * time.Minute, anonymous: true, boundary: true, wantFound: true, wantLater: true},
		{name: "fresh added trip keeps its delay", relationship: gtfsrt.TripDescriptor_ADDED, wantFound: true},
		{name: "fresh duplicated trip keeps its delay", relationship: gtfsrt.TripDescriptor_DUPLICATED, wantFound: true},
		{name: "added trip expires during lookup", relationship: gtfsrt.TripDescriptor_ADDED, recordAge: 5 * time.Minute, boundary: true, wantFound: true, wantLater: true},
		{name: "duplicated trip expires during lookup", relationship: gtfsrt.TripDescriptor_DUPLICATED, recordAge: 5 * time.Minute, boundary: true, wantFound: true, wantLater: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, firstTripID, _ := setupDelayPropTestData(t, api, 1)
			lastStopID := addDownstreamStop(t, api, firstTripID)
			_, err := api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(), "UPDATE stops SET lat = ? WHERE id = ?", 47.01, lastStopID)
			require.NoError(t, err)
			_, err = api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(),
				"UPDATE stop_times SET arrival_time = ?, departure_time = ? WHERE trip_id = ? AND stop_id = ?",
				int64(10*time.Hour), int64(10*time.Hour), firstTripID, lastStopID)
			require.NoError(t, err)
			laterTripID := addLaterBlockTrip(t, api, lastStopID, firstTripID)
			update := plannedVehicleTripUpdate(firstTripID, now.Add(-tt.recordAge))
			delay := 15 * time.Minute
			update.Delay = &delay
			update.ID.ScheduleRelationship = tt.relationship
			vehicleID := plannedVehicleID
			if tt.anonymous {
				update.Vehicle = nil
				vehicleID = delayPropBlockID(t)
			}
			api.GtfsManager.MockAddFullTripUpdate(update)
			if tt.boundary {
				// The entry-time read and construction's expiry check accept the
				// record; the next freshness decision sees it past expiry.
				api.Clock = &recordExpiryBoundaryClock{now: now, freshReads: 2}
			}

			tripID, found := api.activeTripOfTripUpdateVehicle(t.Context(), "dp-agency", firstTripID, vehicleID)

			require.Equal(t, tt.wantFound, found)
			if found {
				wantTripID := firstTripID
				if tt.wantLater {
					wantTripID = laterTripID
				}
				assert.Equal(t, wantTripID, tripID)
			}
		})
	}
}

func TestTripForVehicleHandler_ActiveTripUsesRecordScheduleTime(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(10*time.Hour + time.Minute)
	tests := []struct {
		name      string
		recordAge time.Duration
		delay     time.Duration
	}{
		{name: "record time", recordAge: 2 * time.Minute},
		{name: "schedule deviation", delay: 15 * time.Minute},
		{name: "record time and deviation", recordAge: 2 * time.Minute, delay: 15 * time.Minute},
	}
	for _, anonymous := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s/anonymous=%t", tt.name, anonymous), func(t *testing.T) {
				api := createTestApiWithClock(t, clock.NewMockClock(now))
				defer api.Shutdown()
				t.Cleanup(api.GtfsManager.MockResetRealTimeData)
				_, _, firstTripID, _ := setupDelayPropTestData(t, api, 1)
				lastStopID := addDownstreamStop(t, api, firstTripID)
				_, err := api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(),
					"UPDATE stops SET lat = ? WHERE id = ?", 47.01, lastStopID)
				require.NoError(t, err)
				_, err = api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(),
					"UPDATE stop_times SET arrival_time = ?, departure_time = ? WHERE trip_id = ? AND stop_id = ?",
					int64(10*time.Hour), int64(10*time.Hour), firstTripID, lastStopID)
				require.NoError(t, err)
				blockID := fmt.Sprintf("boundary-%t-%d-%d", anonymous, tt.recordAge, tt.delay)
				_, err = api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(),
					"UPDATE trips SET block_id = ? WHERE id = ?", blockID, firstTripID)
				require.NoError(t, err)
				laterTripID := addLaterBlockTrip(t, api, lastStopID, firstTripID)
				vehicleID := plannedVehicleID
				if anonymous {
					vehicleID = utils.FormCombinedID("dp-agency", blockID)
				}
				for _, tripID := range []string{firstTripID, laterTripID} {
					update := plannedVehicleTripUpdate(tripID, now.Add(-tt.recordAge))
					update.Delay = &tt.delay
					if anonymous {
						update.Vehicle = nil
					}
					api.GtfsManager.MockAddFullTripUpdate(update)
				}

				resp, model := callAPIHandler[TripDetailsResponse](t, api,
					fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", vehicleID))

				require.Equal(t, http.StatusOK, resp.StatusCode)
				wantTripID := utils.FormCombinedID("dp-agency", firstTripID)
				assert.Equal(t, wantTripID, model.Data.Entry.TripID)
				require.NotNil(t, model.Data.Entry.Status)
				assert.Equal(t, wantTripID, model.Data.Entry.Status.ActiveTripID)
			})
		}
	}
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

// countQueries swaps in queryCountingDB (trip_details_benchmark_test.go) for the
// rest of the test.
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
	assert.Zero(t, counter.count.Load())
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
	assert.EqualValues(t, 1, counter.count.Load(), "only the block lookup should run")
}

// A trip-update vehicle positions its block several times per request, for its
// active trip, service date and status, so the handlers share snapshots. Without
// that, trip-for-vehicle ran 89 queries here and trip-details 74.
func TestTripUpdateVehicleHandlers_ShareBlockSnapshots(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute) // before the block's 08:00 start, so the record is kept
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)

	tests := []struct {
		name       string
		endpoint   string
		maxQueries int
	}{
		{name: "trip-for-vehicle", endpoint: fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", plannedVehicleID), maxQueries: 65},
		{name: "trip-details with the vehicle", endpoint: fmt.Sprintf("/api/where/trip-details/dp-agency_%s.json?key=TEST&vehicleId=%s", tripID, plannedVehicleID), maxQueries: 60},
		{name: "arrival-and-departure-for-stop", endpoint: fmt.Sprintf("/api/where/arrival-and-departure-for-stop/dp-agency_dp-stop.json?key=TEST&tripId=dp-agency_%s&serviceDate=%d", tripID, serviceDate.UnixMilli()), maxQueries: 45},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest gets its own rate limiter: the mock clock never refills one.
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now))
			counter := countQueries(t, api)

			resp, _ := callAPIHandler[struct{}](t, api, tt.endpoint)

			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.LessOrEqual(t, int(counter.count.Load()), tt.maxQueries)
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// A block's trip updates form one record, timed by the last one
// (GtfsRealtimeTripLibrary.java:948-950), even when the vehicle comes from the first.
func TestArrivals_TripUpdateVehicleUsesBlockRecordTime(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	stopID := addDownstreamStop(t, api, tripID)
	laterTripID := addLaterBlockTrip(t, api, stopID, tripID)
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now.Add(-20*time.Minute)))
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(laterTripID, now))
	endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
		utils.FormCombinedID("dp-agency", stopID))

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

	arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
	require.NotNil(t, arrival.TripStatus)
	assert.Equal(t, "SCHEDULED", arrival.TripStatus.Status)
	assert.Equal(t, "in_progress", arrival.TripStatus.Phase)
	assert.Equal(t, plannedVehicleID, arrival.VehicleID)
	assert.True(t, arrival.Predicted)
	assert.Equal(t, now.UnixMilli(), arrival.TripStatus.LastUpdateTime.UnixMilli())
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
// adds a location (GtfsRealtimeTripLibrary.java:325, :1101-1102). The canceled
// trip's arrival is hidden whichever vehicle the update names.
func TestCanceledTripUpdateOutranksRealBus(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)
	stopID := addDownstreamStop(t, setupAPI, tripID)
	combinedTripID := utils.FormCombinedID("dp-agency", tripID)

	tests := []struct {
		name          string
		updateVehicle string
		wantStatus    string
	}{
		{name: "update names the bus", updateVehicle: "bus", wantStatus: "CANCELED"},
		{name: "update names no vehicle", wantStatus: "CANCELED"},
		{name: "update names another vehicle", updateVehicle: "other", wantStatus: "SCHEDULED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			tripUpdate := gtfs.Trip{
				ID:        gtfs.TripID{ID: tripID, ScheduleRelationship: gtfsrt.TripDescriptor_CANCELED},
				Timestamp: &now,
			}
			if tt.updateVehicle != "" {
				tripUpdate.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: tt.updateVehicle}}
			}
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

			_, details := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf(
				"/api/where/trip-details/%s.json?key=TEST&serviceDate=%d", combinedTripID, serviceDate.UnixMilli()))
			_, arrivals := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
				"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
				utils.FormCombinedID("dp-agency", stopID)))

			require.NotNil(t, details.Data.Entry.Status)
			assert.Equal(t, tt.wantStatus, details.Data.Entry.Status.Status)
			assertNoArrivalForTrip(t, arrivals.Data.Entry.ArrivalsAndDepartures, combinedTripID)
			original, err := api.GtfsManager.GetVehicleByID("dp-agency", "bus")
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

// Cancellation belongs to one trip, unlike Java, which gives a whole vehicle
// record the first update's status (wiki trip-details, SD-02). The fixture's
// trip A reaches the stop at 09:00 and later trip B at 10:00, on one block.
func TestArrivals_CancellationScopedToTrip(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)

	tests := []struct {
		name            string
		busOnTrip       string // the trip a position-reporting bus serves; empty for a trip-update-only vehicle
		canceledTrip    string
		wantVehicleID   string
		wantRunningTrip string
		wantStatus      string
	}{
		{name: "position bus on B after canceled A", busOnTrip: "B", canceledTrip: "A", wantVehicleID: "dp-agency_bus", wantRunningTrip: "B", wantStatus: "SCHEDULED"},
		{name: "trip-update vehicle after canceled A", canceledTrip: "A", wantVehicleID: plannedVehicleID, wantRunningTrip: "B", wantStatus: "CANCELED"},
		{name: "position bus on A before canceled B", busOnTrip: "A", canceledTrip: "B", wantVehicleID: "dp-agency_bus", wantRunningTrip: "A", wantStatus: "SCHEDULED"},
		{name: "trip-update vehicle before canceled B", canceledTrip: "B", wantVehicleID: plannedVehicleID, wantRunningTrip: "A", wantStatus: "SCHEDULED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripA, _ := setupDelayPropTestData(t, api, 1)
			stopID := addDownstreamStop(t, api, tripA)
			// Apart from the first stop, so the block's distance tells its trips apart.
			_, err := api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(), "UPDATE stops SET lat = ? WHERE id = ?", 47.01, stopID)
			require.NoError(t, err)
			trips := map[string]string{"A": tripA, "B": addLaterBlockTrip(t, api, stopID, tripA)}

			updateVehicleID := plannedVehicleID
			if tt.busOnTrip != "" {
				updateVehicleID = "bus"
				api.GtfsManager.MockAddVehicleWithOptions("bus", trips[tt.busOnTrip], "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			}
			for _, name := range []string{"A", "B"} {
				update := plannedVehicleTripUpdate(trips[name], now)
				update.Vehicle.ID.ID = updateVehicleID
				delay := 2 * time.Minute
				update.Delay = &delay
				if name == tt.canceledTrip {
					update.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
				}
				api.GtfsManager.MockAddFullTripUpdate(update)
			}
			endpoint := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
				utils.FormCombinedID("dp-agency", stopID))

			_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)

			arrivals := model.Data.Entry.ArrivalsAndDepartures
			running := findArrivalForTrip(t, arrivals, utils.FormCombinedID("dp-agency", trips[tt.wantRunningTrip]))
			assert.Equal(t, tt.wantVehicleID, running.VehicleID)
			assert.True(t, running.Predicted, "the running trip keeps its prediction")
			require.NotNil(t, running.TripStatus)
			assert.Equal(t, tt.wantStatus, running.TripStatus.Status, "status follows the active trip")
			assertNoArrivalForTrip(t, arrivals, utils.FormCombinedID("dp-agency", trips[tt.canceledTrip]))

			stopSequences := map[string]int{"A": 5, "B": 1}
			singularCode := func(name string) int {
				resp, _ := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
					"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=%d",
					utils.FormCombinedID("dp-agency", stopID), utils.FormCombinedID("dp-agency", trips[name]),
					serviceDate.UnixMilli(), stopSequences[name]))
				return resp.StatusCode
			}
			assert.Equal(t, http.StatusOK, singularCode(tt.wantRunningTrip))
			assert.Equal(t, http.StatusNotFound, singularCode(tt.canceledTrip))
		})
	}
}

// Only the same vehicle's later update may refresh its record. An anonymous
// update on another trip belongs to the block stand-in, not the named vehicle.
func TestTripForVehicleHandler_RecordRefreshOwnership(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	stopID := addDownstreamStop(t, api, tripID)
	laterTripID := addLaterBlockTrip(t, api, stopID, tripID)
	tests := []struct {
		name      string
		anonymous bool
		wantCode  int
	}{
		{name: "same vehicle refreshes the record", wantCode: http.StatusOK},
		{name: "anonymous block update does not refresh it", anonymous: true, wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now.Add(-20*time.Minute)))
			refresh := plannedVehicleTripUpdate(laterTripID, now)
			if tt.anonymous {
				refresh.Vehicle = nil
			}
			api.GtfsManager.MockAddFullTripUpdate(refresh)

			resp, model := callAPIHandler[TripDetailsResponse](t, api,
				fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", plannedVehicleID))

			require.Equal(t, tt.wantCode, resp.StatusCode)
			if tt.wantCode == http.StatusOK {
				require.NotNil(t, model.Data.Entry.Status)
				assert.Equal(t, plannedVehicleID, model.Data.Entry.Status.VehicleID)
			}
		})
	}
}

// An update canceling another day's run of the trip leaves today's run alone.
func TestArrivals_CancellationForAnotherServiceDate(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	stopID := addDownstreamStop(t, api, tripID)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
	tomorrowCanceled := plannedVehicleTripUpdate(tripID, now)
	tomorrowCanceled.Vehicle.ID.ID = "bus"
	tomorrowCanceled.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
	tomorrowCanceled.ID.HasStartDate = true
	tomorrowCanceled.ID.StartDate = serviceDate.AddDate(0, 0, 1)
	api.GtfsManager.MockAddFullTripUpdate(tomorrowCanceled)

	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
		"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120",
		utils.FormCombinedID("dp-agency", stopID)))

	arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, utils.FormCombinedID("dp-agency", tripID))
	assert.Equal(t, "dp-agency_bus", arrival.VehicleID)
	require.NotNil(t, arrival.TripStatus)
	assert.NotEqual(t, "CANCELED", arrival.TripStatus.Status)
}

// separateBlockStops moves stopID away from the fixture's first stop, so the
// block's distance tells its trips apart.
func separateBlockStops(t *testing.T, api *RestAPI, stopID string) {
	t.Helper()
	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(), "UPDATE stops SET lat = ? WHERE id = ?", 47.01, stopID)
	require.NoError(t, err)
}

// A trip-update vehicle running trip B is not canceled by an update that cancels
// B for another vehicle.
func TestTripUpdateVehicleForTrip_IgnoresAnotherVehiclesCancellation(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(9*time.Hour + 30*time.Minute) // between trip A's end and trip B's start
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripA, _ := setupDelayPropTestData(t, api, 1)
	stopID := addDownstreamStop(t, api, tripA)
	separateBlockStops(t, api, stopID)
	tripB := addLaterBlockTrip(t, api, stopID, tripA)
	busA := plannedVehicleTripUpdate(tripA, now)
	busA.Vehicle.ID.ID = "busA"
	api.GtfsManager.MockAddFullTripUpdate(busA)
	canceledForBusB := plannedVehicleTripUpdate(tripB, now)
	canceledForBusB.Vehicle.ID.ID = "busB"
	canceledForBusB.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
	api.GtfsManager.MockAddFullTripUpdate(canceledForBusB)

	vehicle := api.tripUpdateVehicleForTrip(context.Background(), tripA, serviceDate)

	require.NotNil(t, vehicle)
	assert.Equal(t, "busA", vehicle.ID.ID)
	assert.False(t, isCanceledVehicle(vehicle))
}

// A trip-update vehicle is built from its block's first update, but describes
// the trip its record is running, even when that first update canceled an
// earlier trip.
func TestTripDetails_TripUpdateVehicleDescribesRunningTrip(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(10*time.Hour + 30*time.Minute) // during trip B
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripA, _ := setupDelayPropTestData(t, setupAPI, 1)
	stopID := addDownstreamStop(t, setupAPI, tripA)
	separateBlockStops(t, setupAPI, stopID)
	tripB := addLaterBlockTrip(t, setupAPI, stopID, tripA)

	for _, relationship := range []gtfsrt.TripDescriptor_ScheduleRelationship{gtfsrt.TripDescriptor_SCHEDULED, gtfsrt.TripDescriptor_CANCELED} {
		t.Run(relationship.String(), func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			firstUpdate := plannedVehicleTripUpdate(tripA, now)
			firstUpdate.ID.ScheduleRelationship = relationship
			firstUpdate.StopTimeUpdates = []gtfs.StopTimeUpdate{{StopID: &stopID}}
			api.GtfsManager.MockAddFullTripUpdate(firstUpdate)
			api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripB, now))

			resp, model := callAPIHandler[TripDetailsResponse](t, api, fmt.Sprintf(
				"/api/where/trip-details/%s.json?key=TEST&serviceDate=%d",
				utils.FormCombinedID("dp-agency", tripB), serviceDate.UnixMilli()))

			require.Equal(t, http.StatusOK, resp.StatusCode)
			status := model.Data.Entry.Status
			require.NotNil(t, status)
			assert.Equal(t, "SCHEDULED", status.Status)
			assert.Equal(t, utils.FormCombinedID("dp-agency", tripB), status.ActiveTripID)
			assert.Equal(t, utils.FormCombinedID("dp-agency", stopID), status.NextStop)

			// The status reads its stop times, shape and stop delays from the
			// vehicle's trip, so that must be B rather than A's update.
			vehicle := api.tripUpdateVehicleForTrip(context.Background(), tripB, serviceDate)
			require.NotNil(t, vehicle)
			assert.Equal(t, tripB, vehicle.Trip.ID.ID)
			assert.Empty(t, vehicle.Trip.StopTimeUpdates)
			assert.Equal(t, plannedVehicleID, responseVehicleID("dp-agency", vehicle))
		})
	}
}

// Two vehicles can serve one trip ID, as on a frequency trip, while the trip's
// automatic lookup picks only one. An explicit vehicleId for the other still
// resolves, with the same record checks.
func TestExplicitVehicleIDForSecondVehicleOnTrip(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, stopID, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)
	addDownstreamStop(t, setupAPI, tripID)
	combinedTripID := utils.FormCombinedID("dp-agency", tripID)

	tests := []struct {
		name      string
		recordAge time.Duration
		named     bool
		wantCode  int
	}{
		{name: "fresh anonymous record belongs to busB", wantCode: http.StatusOK},
		{name: "expired anonymous record does not expire busA", recordAge: 20 * time.Minute, wantCode: http.StatusOK},
		{name: "expired named record expires busA", recordAge: 20 * time.Minute, named: true, wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddVehicleWithOptions("busA", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			api.GtfsManager.MockAddVehicleWithOptions("busB", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			update := plannedVehicleTripUpdate(tripID, now.Add(-tt.recordAge))
			update.Vehicle = nil
			if tt.named {
				update.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "busA"}}
			}
			api.GtfsManager.MockAddFullTripUpdate(update)
			query := fmt.Sprintf("key=TEST&serviceDate=%d&vehicleId=dp-agency_busA", serviceDate.UnixMilli())

			detailsResp, details := callAPIHandler[TripDetailsResponse](t, api,
				fmt.Sprintf("/api/where/trip-details/%s.json?%s", combinedTripID, query))
			arrivalResp, arrival := callAPIHandler[ArrivalAndDepartureResponse](t, api,
				fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?%s&tripId=%s&stopSequence=1", stopID, query, combinedTripID))

			require.Equal(t, tt.wantCode, detailsResp.StatusCode)
			require.Equal(t, tt.wantCode, arrivalResp.StatusCode)
			if tt.wantCode != http.StatusOK {
				return
			}
			require.NotNil(t, details.Data.Entry.Status)
			assert.Equal(t, "dp-agency_busA", details.Data.Entry.Status.VehicleID)
			assert.Equal(t, "dp-agency_busA", arrival.Data.Entry.VehicleID)
		})
	}
}

// Every arrival row on a block asks for its block's trips, through the vehicle
// lookup, the cancellation check and the deviation, so a request builds the list
// once per trip and service date.
func TestBlockShiftTripIDs_CachedPerRequest(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(serviceDate.Add(8*time.Hour)))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	stopID := addDownstreamStop(t, api, tripID)
	laterTripID := addLaterBlockTrip(t, api, stopID, tripID)
	ctx := WithSnapshotCache(context.Background(), newSnapshotCache())
	first := api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate)
	counter := countQueries(t, api)

	again := api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate)

	assert.Zero(t, counter.count.Load())
	assert.Equal(t, []string{tripID, laterTripID}, first)
	assert.Equal(t, first, again)
}

func TestBlockShiftTripIDs_SharedWithShiftSiblings(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(serviceDate.Add(8*time.Hour)))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	stopID := addDownstreamStop(t, api, tripID)
	laterTripID := addLaterBlockTrip(t, api, stopID, tripID)
	// Populate cached bounds (normally written by GTFS import) so shift resolution completes cleanly.
	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(context.Background(), `
		UPDATE trips
		SET min_arrival_time = (SELECT MIN(arrival_time) FROM stop_times WHERE stop_times.trip_id = trips.id),
		    max_departure_time = (SELECT MAX(departure_time) FROM stop_times WHERE stop_times.trip_id = trips.id)
		WHERE id IN (?, ?)`, tripID, laterTripID)
	require.NoError(t, err)
	ctx := WithSnapshotCache(context.Background(), newSnapshotCache())

	first := api.blockShiftTripIDsSortedByStartTime(ctx, tripID, serviceDate)
	first[0] = "mutated" // Verify returned slice is cloned.

	counter := countQueries(t, api)
	siblingResult := api.blockShiftTripIDsSortedByStartTime(ctx, laterTripID, serviceDate)

	assert.Zero(t, counter.count.Load())
	assert.Equal(t, []string{tripID, laterTripID}, siblingResult)
}

// A block's updates expire together as one record, so a trip's own stale update
// still predicts its stops while a later update keeps the record fresh.
func TestArrivals_StaleUpdateInFreshRecordStillPredicts(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripA, _ := setupDelayPropTestData(t, setupAPI, 1)
	stopID := addDownstreamStop(t, setupAPI, tripA)
	tripB := addLaterBlockTrip(t, setupAPI, stopID, tripA)
	combinedTripID := utils.FormCombinedID("dp-agency", tripA)
	combinedStopID := utils.FormCombinedID("dp-agency", stopID)
	predictedArrival := serviceDate.Add(9*time.Hour + 3*time.Minute)
	predictedDeparture := serviceDate.Add(9*time.Hour + 12*time.Minute)

	tests := []struct {
		name     string
		arrivals func(api *RestAPI) models.ArrivalAndDeparture
	}{
		{name: "plural", arrivals: func(api *RestAPI) models.ArrivalAndDeparture {
			_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
				"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=0&minutesAfter=120", combinedStopID))
			return findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, combinedTripID)
		}},
		{name: "singular", arrivals: func(api *RestAPI) models.ArrivalAndDeparture {
			_, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
				"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=5",
				combinedStopID, combinedTripID, serviceDate.UnixMilli()))
			return model.Data.Entry
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			staleUpdate := plannedVehicleTripUpdate(tripA, now.Add(-6*time.Minute))
			staleUpdate.StopTimeUpdates = []gtfs.StopTimeUpdate{{
				StopID:    &stopID,
				Arrival:   &gtfs.StopTimeEvent{Time: &predictedArrival},
				Departure: &gtfs.StopTimeEvent{Time: &predictedDeparture},
			}}
			api.GtfsManager.MockAddFullTripUpdate(staleUpdate)
			api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripB, now))

			arrival := tt.arrivals(api)

			assert.True(t, arrival.Predicted)
			assert.Equal(t, predictedArrival.UnixMilli(), arrival.PredictedArrivalTime.UnixMilli())
			assert.Equal(t, predictedDeparture.UnixMilli(), arrival.PredictedDepartureTime.UnixMilli())
		})
	}
}

// Vehicle IDs are only unique within an agency, so a position vehicle at another
// agency does not stand in for a trip-update vehicle sharing its raw ID.
func TestTripUpdateVehicleForTrip_PositionVehicleAtAnotherAgency(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)
	addDownstreamStop(t, setupAPI, tripID)

	tests := []struct {
		name          string
		vehicleAgency string
		wantVehicle   bool
	}{
		{name: "another agency's position vehicle", vehicleAgency: "other-agency", wantVehicle: true},
		{name: "this agency's position vehicle", vehicleAgency: "dp-agency"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			latitude, longitude := float32(40.0), float32(-120.0)
			positionVehicle := gtfs.Vehicle{
				ID:                &gtfs.VehicleID{ID: "101"},
				Trip:              &gtfs.Trip{ID: gtfs.TripID{ID: "another-trip"}},
				Position:          &gtfs.Position{Latitude: &latitude, Longitude: &longitude},
				Timestamp:         &now,
				IsEntityInMessage: true,
			}
			api.GtfsManager.MockSetAgencyVehicles("_test", tt.vehicleAgency, []gtfs.Vehicle{positionVehicle})
			update := plannedVehicleTripUpdate(tripID, now)
			update.Vehicle.ID.ID = "101"
			api.GtfsManager.MockAddFullTripUpdate(update)

			vehicle := api.tripUpdateVehicleForTrip(context.Background(), tripID, serviceDate)

			assert.Equal(t, tt.wantVehicle, vehicle != nil)
		})
	}
}

// An agency ID can contain an underscore, so the explicit vehicle ID is read
// after the route's agency prefix rather than split at its first underscore.
func TestTripForVehicleHandler_RawIDWithUnrelatedPositionSuffix(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(7*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, stopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	_, err := api.GtfsManager.GtfsDB.Queries.CreateTrip(t.Context(), gtfsdb.CreateTripParams{
		ID: "unrelated-suffix-trip", RouteID: "dp-route", ServiceID: "dp-svc", BlockID: nulls.String("other-block"),
	})
	require.NoError(t, err)
	api.GtfsManager.MockAddVehicle("9_schedBasedVehicle", "unrelated-suffix-trip", "dp-route")
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(tripID, now))
	_, arrivals := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
		"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=5", stopID))
	foundArrival := false
	for _, arrival := range arrivals.Data.Entry.ArrivalsAndDepartures {
		if arrival.TripID == utils.FormCombinedID("dp-agency", tripID) {
			require.Equal(t, plannedVehicleID, arrival.VehicleID)
			foundArrival = true
		}
	}
	require.True(t, foundArrival, "the trip-update vehicle's arrival must be returned")
	response, details := callAPIHandler[TripDetailsResponse](t, api,
		fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", plannedVehicleID))
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NotNil(t, details.Data.Entry.Status)
	assert.Equal(t, plannedVehicleID, details.Data.Entry.Status.VehicleID)
}

func TestRequestedVehicleForTrip_AgencyIDWithUnderscore(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	tripID := "underscore-agency-trip"
	api.GtfsManager.MockAddAgency("AGENCY_ONE", "Underscore agency")
	api.GtfsManager.MockAddRoute("underscore-agency-route", "AGENCY_ONE", "Underscore route")
	api.GtfsManager.MockAddVehicleWithOptions("busA", tripID, "underscore-agency-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
	api.GtfsManager.MockAddVehicleWithOptions("busB", tripID, "underscore-agency-route", internalgtfs.MockVehicleOptions{Timestamp: &now})

	ctx := context.Background()
	otherVehicleID := "busA"
	if automatic := api.vehicleForTrip(ctx, tripID, serviceDate); automatic.ID.ID == "busA" {
		otherVehicleID = "busB"
	}

	vehicle := api.requestedVehicleForTrip(ctx, "AGENCY_ONE_"+otherVehicleID, "AGENCY_ONE", tripID, serviceDate)

	require.NotNil(t, vehicle)
	assert.Equal(t, otherVehicleID, vehicle.ID.ID)
}

// Each named update on a block checks its trip's agency while building an
// arrival row's vehicle, so a request looks each trip's agency up once.
func TestTripAgencyID_CachedPerRequest(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(serviceDate.Add(8*time.Hour)))
	defer api.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	ctx := WithSnapshotCache(context.Background(), newSnapshotCache())
	first, firstKnown := api.tripAgencyID(ctx, tripID)
	counter := countQueries(t, api)

	again, againKnown := api.tripAgencyID(ctx, tripID)

	assert.Zero(t, counter.count.Load())
	assert.True(t, firstKnown)
	assert.Equal(t, "dp-agency", first)
	assert.Equal(t, againKnown, firstKnown)
	assert.Equal(t, first, again)
}

// trip-for-vehicle reports a vehicle only while Java holds a record for it, as
// trip-details and the arrivals do. A vehicle go-gtfs builds from a trip update
// at the vehicle positions URL takes the trip-update path, with its record checks.
func TestTripForVehicleHandler_RecordChecks(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + time.Minute)
	setupAPI := createTestApiWithClock(t, clock.NewMockClock(now))
	defer setupAPI.Shutdown()
	_, _, tripID, _ := setupDelayPropTestData(t, setupAPI, 1)

	addPositionVehicle := func(api *RestAPI, _ gtfs.Trip) {
		api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
	}
	addLibraryVehicle := func(api *RestAPI, tripUpdate gtfs.Trip) {
		tripUpdate.IsEntityInMessage = true
		api.GtfsManager.MockSetAgencyVehicles("_test", "dp-agency", []gtfs.Vehicle{{ID: tripUpdate.Vehicle.ID, Trip: &tripUpdate}})
	}
	tests := []struct {
		name       string
		addVehicle func(*RestAPI, gtfs.Trip)
		requestID  string
		recordAge  time.Duration
		wantCode   int
	}{
		{name: "position vehicle with a fresh record", addVehicle: addPositionVehicle, requestID: "dp-agency_bus", wantCode: http.StatusOK},
		{name: "position vehicle with an expired record", addVehicle: addPositionVehicle, requestID: "dp-agency_bus", recordAge: 20 * time.Minute, wantCode: http.StatusNotFound},
		{name: "library trip-update vehicle with a fresh record", addVehicle: addLibraryVehicle, requestID: "dp-agency_bus", wantCode: http.StatusOK},
		{name: "library trip-update vehicle with an expired record", addVehicle: addLibraryVehicle, requestID: "dp-agency_bus", recordAge: 20 * time.Minute, wantCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			tripUpdate := plannedVehicleTripUpdate(tripID, now.Add(-tt.recordAge))
			tripUpdate.Vehicle.ID.ID = "bus"
			tt.addVehicle(api, tripUpdate)
			api.GtfsManager.MockAddFullTripUpdate(tripUpdate)

			resp, _ := callAPIHandler[struct{}](t, api, fmt.Sprintf("/api/where/trip-for-vehicle/%s.json?key=TEST", tt.requestID))

			assert.Equal(t, tt.wantCode, resp.StatusCode)
		})
	}
}

func TestBuildTripStatus_PassedVehicleWithExpiredRecord(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	tests := []struct {
		name         string
		relationship gtfs.TripScheduleRelationship
		canceled     bool
		recordAge    time.Duration
		wantStatus   string
		wantRecord   bool
	}{
		{name: "scheduled vehicle survives expired record", recordAge: 6 * time.Minute, wantStatus: "SCHEDULED"},
		{name: "duplicated vehicle survives expired record", relationship: gtfsrt.TripDescriptor_DUPLICATED, recordAge: 6 * time.Minute, wantStatus: "DUPLICATED"},
		{name: "expired cancellation does not change the vehicle", canceled: true, recordAge: 6 * time.Minute, wantStatus: "SCHEDULED"},
		{name: "live cancellation still applies", canceled: true, recordAge: time.Minute, wantStatus: "CANCELED", wantRecord: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, tripID)
			lat, lon := float32(47), float32(-122)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{
				Timestamp: &now, Position: &gtfs.Position{Latitude: &lat, Longitude: &lon}, ScheduleRelationship: tt.relationship,
			})
			vehicle := api.GtfsManager.GetVehicleForTrip(context.Background(), tripID)
			require.NotNil(t, vehicle)
			recordTime, delay := now.Add(-tt.recordAge), 10*time.Minute
			update := gtfs.Trip{
				ID: gtfs.TripID{ID: tripID, ScheduleRelationship: tt.relationship}, Timestamp: &recordTime, Delay: &delay,
				Vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
			}
			if tt.canceled {
				update.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
			}
			api.GtfsManager.MockAddFullTripUpdate(update)

			status, extras, err := api.BuildTripStatus(context.Background(), "dp-agency", tripID, vehicle, serviceDate, now, nil)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, status.Status)
			assert.True(t, status.Predicted, "fresh GPS remains real-time data after its record expires")
			assert.Equal(t, "dp-agency_bus", status.VehicleID)
			assert.Equal(t, tt.wantRecord, extras.record.present)
			assert.Zero(t, status.ScheduleDeviation, "expired/canceled record delay must not leak into the position-only status")
			if tt.wantRecord {
				assert.Equal(t, recordTime.UnixMilli(), status.LastUpdateTime.UnixMilli())
				assert.Empty(t, status.Phase)
				assert.Zero(t, status.DistanceAlongTrip)
			} else {
				assert.Equal(t, now.UnixMilli(), status.LastUpdateTime.UnixMilli())
				assert.Equal(t, "in_progress", status.Phase)
				require.NotNil(t, status.LastKnownLocation)
				assert.Equal(t, float64(lat), status.LastKnownLocation.Lat)
				assert.Nil(t, api.vehicleForTrip(context.Background(), tripID, serviceDate), "automatic lookup must still reject the expired record")
			}
			assert.Equal(t, tt.relationship, vehicle.Trip.ID.ScheduleRelationship, "do not mutate the shared vehicle")
		})
	}
}

func TestBuildTripStatus_PassedTripUpdateVehicleRequiresFreshRecord(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	for _, anonymous := range []bool{false, true} {
		t.Run(fmt.Sprintf("anonymous=%t", anonymous), func(t *testing.T) {
			mockClock := clock.NewMockClock(now)
			api := createTestApiWithClock(t, mockClock)
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, tripID)
			update := plannedVehicleTripUpdate(tripID, now.Add(-time.Minute))
			if anonymous {
				update.Vehicle = nil
			}
			api.GtfsManager.MockAddFullTripUpdate(update)
			vehicle := api.tripUpdateVehicleForTrip(t.Context(), tripID, serviceDate)
			require.NotNil(t, vehicle)
			require.True(t, isTripUpdateVehicle(vehicle))
			require.Nil(t, vehicle.Position)
			status, extras, err := api.BuildTripStatus(t.Context(), "dp-agency", tripID, vehicle, serviceDate, now, nil)
			require.NoError(t, err)
			require.True(t, status.Predicted)
			require.True(t, extras.record.present)

			// The record is expired but its timestamp is still inside the
			// position vehicle's longer stale window.
			mockClock.Advance(6 * time.Minute)
			status, extras, err = api.BuildTripStatus(t.Context(), "dp-agency", tripID, vehicle, serviceDate, mockClock.Now(), nil)

			require.NoError(t, err)
			assert.False(t, extras.record.present)
			assert.False(t, status.Predicted)
			assert.True(t, status.Scheduled)
			assert.Equal(t, "default", status.Status)
			assert.Equal(t, "scheduled", status.Phase)
			assert.Empty(t, status.VehicleID)
			assert.True(t, status.LastUpdateTime.IsZero())
		})
	}
}

func TestVehiclesForAgency_ExpiredRecordKeepsLiveVehicleStatus(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	addDownstreamStop(t, api, tripID)
	lat, lon := float32(47), float32(-122)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{
		Timestamp: &now, Position: &gtfs.Position{Latitude: &lat, Longitude: &lon},
	})
	expired, delay := now.Add(-6*time.Minute), 10*time.Minute
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
		ID: gtfs.TripID{ID: tripID}, Timestamp: &expired, Delay: &delay,
		Vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
	})

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, "/api/where/vehicles-for-agency/dp-agency.json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	entry := findVehicleStatusByID(model.Data.List, "bus")
	require.NotNil(t, entry)
	require.NotNil(t, entry.Location)
	require.NotNil(t, entry.TripStatus)
	assert.True(t, entry.TripStatus.Predicted)
	assert.False(t, entry.TripStatus.Scheduled)
	assert.Equal(t, "SCHEDULED", entry.TripStatus.Status)
	assert.Equal(t, "in_progress", entry.TripStatus.Phase)
	assert.Equal(t, now.UnixMilli(), entry.TripStatus.LastUpdateTime.UnixMilli())
	assert.Zero(t, entry.TripStatus.ScheduleDeviation)
}

func TestVehicleRecordOwnership_DeviationSamples(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	stopID, _, tripID, _ := setupDelayPropTestData(t, api, 1)
	addDownstreamStop(t, api, tripID)
	laterID := addLaterBlockTrip(t, api, stopID, tripID)
	cache := deviationSampleCache{}
	for i, id := range []string{tripID, laterID} {
		delay := time.Duration(i*9+1) * time.Minute
		sequence := uint32(0)
		api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
			ID: gtfs.TripID{ID: id}, Timestamp: &now,
			Vehicle:         &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: id}},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &stopID, StopSequence: &sequence, Departure: &gtfs.StopTimeEvent{Delay: &delay}}},
		})
	}
	for _, id := range []string{tripID, laterID, tripID} {
		vehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: id}, Trip: &gtfs.Trip{ID: gtfs.TripID{ID: id}}, Timestamp: &now}
		status, extras, err := api.BuildTripStatus(context.Background(), "dp-agency", id, vehicle, serviceDate, now, nil)
		require.NoError(t, err)
		deviation, propagates := api.deviationForStop(context.Background(), scheduleDeviationFallback{
			status: status, extras: extras, sampleCache: cache, serviceMidnight: serviceDate,
			currentTime: now, stopArrivalSeconds: 11 * 60 * 60,
		})
		require.True(t, propagates)
		want := 60
		if id == laterID {
			want = 600
		}
		assert.Equal(t, want, status.ScheduleDeviation)
		assert.Equal(t, want, deviation)
	}
}

func TestVehicleRecordOwnership_RetentionAndDeviation(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	tests := []struct {
		name        string
		age         time.Duration
		anonymous   bool
		position    bool
		wantVehicle bool
	}{
		{name: "fresh position record", age: time.Minute, position: true, wantVehicle: true},
		{name: "expired position record", age: 10 * time.Minute, position: true},
		{name: "anonymous position record", age: time.Minute, anonymous: true, position: true, wantVehicle: true},
		{name: "expired anonymous position record", age: 10 * time.Minute, anonymous: true, position: true},
		{name: "fresh trip-update record", age: time.Minute, wantVehicle: true},
		{name: "expired trip-update record", age: 10 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			stopID := addDownstreamStop(t, api, tripID)
			laterID := addLaterBlockTrip(t, api, stopID, tripID)
			ownTime := now.Add(-tt.age)
			ownDelay, otherDelay := time.Minute, 10*time.Minute
			ownUpdate := gtfs.Trip{ID: gtfs.TripID{ID: tripID}, Timestamp: &ownTime, Delay: &ownDelay}
			if !tt.anonymous {
				ownUpdate.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}}
			}
			api.GtfsManager.MockAddFullTripUpdate(ownUpdate)
			api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
				ID: gtfs.TripID{ID: laterID}, Timestamp: &now, Delay: &otherDelay,
				Vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "other"}},
			})
			// Keep the tested bus last so it owns anonymous updates. The other
			// bus has a position and must not be synthesized for an expired record.
			api.GtfsManager.MockAddVehicleWithOptions("other", laterID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			if tt.position {
				api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			}

			vehicle := api.vehicleForTrip(context.Background(), tripID, serviceDate)
			if !tt.wantVehicle {
				require.Nil(t, vehicle)
			} else {
				require.NotNil(t, vehicle)
				require.Equal(t, "bus", vehicle.ID.ID)
			}
			status, extras, err := api.BuildTripStatus(context.Background(), "dp-agency", tripID, nil, serviceDate, now, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVehicle, extras.record.present)
			assert.Equal(t, tt.wantVehicle, status.Predicted)
			_, _, predicted := api.getPredictedTimes(context.Background(), tripID, stopID, 5,
				scheduledTimes{arrival: serviceDate.Add(9 * time.Hour), departure: serviceDate.Add(9*time.Hour + 5*time.Minute)}, predictionClock{serviceDate: serviceDate})
			assert.Equal(t, tt.wantVehicle, predicted, "arrival predictions must use the same record expiry")
			if tt.wantVehicle {
				assert.Equal(t, 60, status.ScheduleDeviation)
				assert.Equal(t, ownTime.UnixMilli(), status.LastUpdateTime.UnixMilli())
			} else {
				assert.Zero(t, status.ScheduleDeviation)
			}
		})
	}
}

func TestVehicleRecordOwnership_OnePositionVehiclePerBlock(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	for _, anonymousFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("anonymousFirst=%t", anonymousFirst), func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			stopID, _, firstID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, firstID)
			nextID := addLaterBlockTrip(t, api, stopID, firstID)
			firstTime, gpsTime := now.Add(-time.Minute), now.Add(-30*time.Second)
			firstDelay, nextDelay := time.Minute, 10*time.Minute
			first := gtfs.Trip{ID: gtfs.TripID{ID: firstID}, Timestamp: &firstTime, Delay: &firstDelay}
			if !anonymousFirst {
				first.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "first-bus"}}
			}
			next := gtfs.Trip{ID: gtfs.TripID{ID: nextID}, Timestamp: &now, Delay: &nextDelay}
			api.GtfsManager.MockAddFullTripUpdate(first)
			api.GtfsManager.MockAddFullTripUpdate(next)
			// Java's last position entity for the block wins the anonymous assignment.
			api.GtfsManager.MockAddVehicleWithOptions("first-bus", firstID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &gpsTime})
			api.GtfsManager.MockAddVehicleWithOptions("next-bus", nextID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &gpsTime})
			ctx := WithSnapshotCache(t.Context(), newSnapshotCache())
			for _, vehicle := range api.GtfsManager.GetRealTimeVehicles() {
				status, extras, err := api.BuildTripStatus(ctx, "dp-agency", vehicle.Trip.ID.ID, &vehicle, serviceDate, now, nil)
				require.NoError(t, err)
				if vehicle.ID.ID == "next-bus" {
					assert.True(t, extras.record.present)
					assert.Equal(t, now.UnixMilli(), status.LastUpdateTime.UnixMilli())
					assert.Equal(t, 600, status.ScheduleDeviation)
				} else if anonymousFirst {
					assert.False(t, extras.record.present, "the unassigned bus must stay position-only")
					assert.Equal(t, gpsTime.UnixMilli(), status.LastUpdateTime.UnixMilli())
					assert.Zero(t, status.ScheduleDeviation)
				} else {
					assert.True(t, extras.record.present)
					assert.Equal(t, firstTime.UnixMilli(), status.LastUpdateTime.UnixMilli())
					assert.Equal(t, 60, status.ScheduleDeviation)
				}
			}
			for _, id := range []string{firstID, nextID} {
				vehicle := api.vehicleForTrip(ctx, id, serviceDate)
				require.NotNil(t, vehicle)
				wantID := "next-bus"
				if id == firstID && !anonymousFirst {
					wantID = "first-bus"
				}
				assert.Equal(t, wantID, vehicle.ID.ID, "lookup must follow the anonymous assignment even with a direct position on that trip")
			}
			assert.Nil(t, api.tripUpdateVehicleForTrip(ctx, nextID, serviceDate), "assigned anonymous updates must not also create a stand-in")
			updates := api.predictionRecordUpdates(ctx, next, nextID, serviceDate)
			wantCount := 1
			if anonymousFirst {
				wantCount = 2
			}
			assert.Len(t, updates, wantCount)
			if anonymousFirst {
				updateOnly := first
				updateOnly.IsEntityInMessage = true
				updateOnly.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "first-bus"}}
				vehicle := &gtfs.Vehicle{ID: updateOnly.Vehicle.ID, Trip: &updateOnly}
				assert.Empty(t, api.vehicleRecordUpdates(ctx, api.tripBlockUpdates(ctx, firstID, serviceDate), vehicle, serviceDate),
					"an update-only vehicle must not also consume assigned anonymous updates")
			}
		})
	}
}

func TestVehicleRecordTime_ScopedToVehicle(t *testing.T) {
	now := time.Date(2010, 1, 1, 8, 30, 0, 0, time.UTC)
	ownTime := now.Add(-time.Minute)
	expiredTime := now.Add(-10 * time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	stopID, _, firstID, _ := setupDelayPropTestData(t, api, 1)
	nextID := addLaterBlockTrip(t, api, stopID, firstID)
	update := func(tripID, vehicleID string, timestamp time.Time) gtfs.Trip {
		if tripID == "A" {
			tripID = firstID
		} else {
			tripID = nextID
		}
		trip := gtfs.Trip{ID: gtfs.TripID{ID: tripID}, Timestamp: &timestamp, IsEntityInMessage: true}
		if vehicleID != "" {
			trip.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: vehicleID}}
		}
		return trip
	}
	ownUpdate := update("A", "bus", ownTime)
	anonymousUpdate := update("A", "", ownTime)
	positionVehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}, Trip: &gtfs.Trip{ID: ownUpdate.ID}, IsEntityInMessage: true}
	blockVehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "block"}, Trip: &anonymousUpdate, Timestamp: &now}
	updateVehicle := &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}, Trip: &ownUpdate, Timestamp: &now}
	tests := []struct {
		name    string
		vehicle *gtfs.Vehicle
		updates []gtfs.Trip
		want    time.Time
		found   bool
	}{
		{name: "another named vehicle", vehicle: positionVehicle, updates: []gtfs.Trip{ownUpdate, update("B", "other", now)}, want: ownTime, found: true},
		{name: "anonymous update on own trip", vehicle: positionVehicle, updates: []gtfs.Trip{anonymousUpdate}, want: ownTime, found: true},
		{name: "anonymous update without a vehicle trip", vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}}, updates: []gtfs.Trip{anonymousUpdate}},
		{name: "anonymous block update refreshes a position vehicle", vehicle: positionVehicle, updates: []gtfs.Trip{ownUpdate, update("B", "", now)}, want: now, found: true},
		{name: "only anonymous update on another trip", vehicle: positionVehicle, updates: []gtfs.Trip{update("B", "", now)}, want: now, found: true},
		{name: "anonymous block update does not refresh an update-only vehicle", vehicle: updateVehicle, updates: []gtfs.Trip{ownUpdate, update("B", "", now)}, want: ownTime, found: true},
		{name: "another named vehicle on own trip", vehicle: positionVehicle, updates: []gtfs.Trip{update("A", "other", now)}},
		{name: "expired anonymous update on own trip", vehicle: positionVehicle, updates: []gtfs.Trip{update("A", "", expiredTime)}, want: expiredTime},
		{name: "same vehicle uses last block update", vehicle: positionVehicle, updates: []gtfs.Trip{ownUpdate, update("B", "bus", now)}, want: now, found: true},
		{name: "block vehicle ignores named updates", vehicle: blockVehicle, updates: []gtfs.Trip{anonymousUpdate, update("B", "other", now)}, want: ownTime, found: true},
		{name: "block vehicle uses last anonymous update", vehicle: blockVehicle, updates: []gtfs.Trip{anonymousUpdate, update("B", "", now)}, want: now, found: true},
		{name: "another vehicle cannot refresh an expired record", vehicle: positionVehicle, updates: []gtfs.Trip{update("A", "bus", expiredTime), update("B", "other", now)}, want: expiredTime},
		{name: "no matching record", vehicle: positionVehicle, updates: []gtfs.Trip{update("B", "other", now)}},
		{name: "fallback uses own trip timestamp", vehicle: updateVehicle, updates: []gtfs.Trip{update("B", "other", now)}, want: ownTime, found: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			for _, trip := range tt.updates {
				api.GtfsManager.MockAddFullTripUpdate(trip)
			}

			if tt.vehicle == positionVehicle || tt.vehicle.Trip == nil {
				api.GtfsManager.MockAddVehicleWithOptions("bus", firstID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
			}
			got, found := api.vehicleRecordTime(t.Context(), tt.vehicle, []string{firstID, nextID}, now.Truncate(24*time.Hour))

			assert.Equal(t, tt.found, found)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestArrivals_AnonymousBlockUpdatesKeepPositionVehicle(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	tests := []struct {
		name      string
		firstAge  time.Duration
		nextAge   time.Duration
		omitFirst bool
		nameFirst bool
		emptyID   bool
		nameNext  bool
		wantID    string
	}{
		{name: "anonymous block updates", firstAge: time.Minute, nextAge: 2 * time.Minute, wantID: "bus"},
		{name: "next trip refreshes expired first update", firstAge: 10 * time.Minute, nextAge: time.Minute, wantID: "bus"},
		{name: "only next trip has an update", omitFirst: true, nextAge: time.Minute, wantID: "bus"},
		{name: "empty vehicle IDs", firstAge: time.Minute, nextAge: 2 * time.Minute, emptyID: true, wantID: "bus"},
		{name: "named first update and anonymous next trip", firstAge: time.Minute, nextAge: 2 * time.Minute, nameFirst: true, wantID: "bus"},
		{name: "expired next update expires the combined record", firstAge: time.Minute, nextAge: 10 * time.Minute, nameFirst: true},
		{name: "all block updates expired", firstAge: 10 * time.Minute, nextAge: 9 * time.Minute},
		{name: "next trip names another bus", firstAge: time.Minute, nextAge: 2 * time.Minute, nameNext: true, wantID: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			stopID, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, tripID)
			nextID := addLaterBlockTrip(t, api, stopID, tripID)
			firstTime, nextTime := now.Add(-tt.firstAge), now.Add(-tt.nextAge)
			first := gtfs.Trip{ID: gtfs.TripID{ID: tripID}, Timestamp: &firstTime}
			next := gtfs.Trip{ID: gtfs.TripID{ID: nextID}, Timestamp: &nextTime}
			if tt.nameFirst {
				first.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}}
			}
			if tt.nameNext {
				next.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "other"}}
			}
			if tt.emptyID {
				first.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{}}
				next.Vehicle = &gtfs.Vehicle{ID: &gtfs.VehicleID{}}
			}
			if !tt.omitFirst {
				api.GtfsManager.MockAddFullTripUpdate(first)
			}
			api.GtfsManager.MockAddFullTripUpdate(next)
			gpsTime := now.Add(-30 * time.Second)
			lat, lon := float32(47), float32(-122)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{
				Timestamp: &gpsTime, Position: &gtfs.Position{Latitude: &lat, Longitude: &lon},
			})

			vehicle := api.vehicleForTrip(t.Context(), nextID, serviceDate)
			if tt.wantID == "" {
				require.Nil(t, vehicle)
			} else {
				require.NotNil(t, vehicle)
				assert.Equal(t, tt.wantID, vehicle.ID.ID)
			}
			combinedTripID := utils.FormCombinedID("dp-agency", nextID)
			if tt.wantID == "bus" {
				updates := api.predictionRecordUpdates(t.Context(), next, nextID, serviceDate)
				wantCount := 2
				if tt.omitFirst {
					wantCount = 1
				}
				assert.Len(t, updates, wantCount, "prediction expiry and deviation must use the same combined record")
			}
			// Java overwrites record time in block order, not timestamp order.
			wantRecordTime := nextTime
			check := func(t *testing.T, arrival models.ArrivalAndDeparture) {
				t.Helper()
				require.NotNil(t, arrival.TripStatus)
				assert.Equal(t, tt.wantID != "", arrival.Predicted)
				if tt.wantID == "" {
					assert.Empty(t, arrival.VehicleID)
					assert.Empty(t, arrival.TripStatus.VehicleID)
					return
				}
				wantVehicleID := utils.FormCombinedID("dp-agency", tt.wantID)
				assert.Equal(t, wantVehicleID, arrival.VehicleID)
				assert.Equal(t, wantVehicleID, arrival.TripStatus.VehicleID)
				assert.Equal(t, wantRecordTime.UnixMilli(), arrival.LastUpdateTime.UnixMilli())
				assert.Equal(t, wantRecordTime.UnixMilli(), arrival.TripStatus.LastUpdateTime.UnixMilli())
				if tt.wantID == "bus" {
					require.NotNil(t, arrival.TripStatus.LastKnownLocation)
					assert.Equal(t, float64(lat), arrival.TripStatus.LastKnownLocation.Lat)
					assert.Equal(t, float64(lon), arrival.TripStatus.LastKnownLocation.Lon)
					assert.Equal(t, gpsTime.UnixMilli(), arrival.TripStatus.LastLocationUpdateTime.UnixMilli())
				}
			}
			t.Run("singular", func(t *testing.T) {
				resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
					"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
					combinedStopID, combinedTripID, serviceDate.UnixMilli()))
				require.Equal(t, http.StatusOK, resp.StatusCode)
				check(t, model.Data.Entry)
			})
			t.Run("plural", func(t *testing.T) {
				resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
					"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=60&minutesAfter=180", combinedStopID))
				require.Equal(t, http.StatusOK, resp.StatusCode)
				check(t, findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, combinedTripID))
			})
		})
	}
}

func TestArrivals_RecordTimeIgnoresOtherBlockVehicle(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	recordTime := serviceDate.Add(8*time.Hour + 30*time.Minute)
	otherRecordTime := recordTime.Add(4 * time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(otherRecordTime))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	laterTripID := addLaterBlockTrip(t, api, "dp-stop", tripID)
	positionTime := recordTime.Add(-20 * time.Second)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &positionTime})
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
		ID:        gtfs.TripID{ID: tripID},
		Timestamp: &recordTime,
		Vehicle:   &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
	})
	api.GtfsManager.MockAddFullTripUpdate(plannedVehicleTripUpdate(laterTripID, otherRecordTime))
	combinedTripID := utils.FormCombinedID("dp-agency", tripID)

	t.Run("singular", func(t *testing.T) {
		resp, body := serveApiAndRetrieveEndpoint(t, api, fmt.Sprintf(
			"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
			combinedStopID, combinedTripID, serviceDate.UnixMilli()))

		require.Equal(t, http.StatusOK, resp.StatusCode)
		entry := body.Data.(map[string]any)["entry"].(map[string]any)
		assert.EqualValues(t, recordTime.UnixMilli(), entry["lastUpdateTime"])
	})
	t.Run("plural", func(t *testing.T) {
		resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
			"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=60&minutesAfter=120", combinedStopID))

		require.Equal(t, http.StatusOK, resp.StatusCode)
		arrival := findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, combinedTripID)
		assert.Equal(t, recordTime.UnixMilli(), arrival.LastUpdateTime.UnixMilli())
	})
}

func TestArrivals_StalePositionWithFreshRecord(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	addDownstreamStop(t, api, tripID)
	gpsTime := now.Add(-16 * time.Minute)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &gpsTime})
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
		ID: gtfs.TripID{ID: tripID}, Timestamp: &now,
		Vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
	})
	combinedTripID := utils.FormCombinedID("dp-agency", tripID)
	check := func(t *testing.T, arrival models.ArrivalAndDeparture) {
		t.Helper()
		require.NotNil(t, arrival.TripStatus)
		assert.Equal(t, "SCHEDULED", arrival.TripStatus.Status)
		assert.Equal(t, "in_progress", arrival.TripStatus.Phase)
		assert.True(t, arrival.TripStatus.Predicted)
		assert.Equal(t, now.UnixMilli(), arrival.LastUpdateTime.UnixMilli())
		assert.Equal(t, now.UnixMilli(), arrival.TripStatus.LastUpdateTime.UnixMilli())
	}
	t.Run("singular", func(t *testing.T) {
		resp, model := callAPIHandler[ArrivalAndDepartureResponse](t, api, fmt.Sprintf(
			"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
			combinedStopID, combinedTripID, serviceDate.UnixMilli()))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		check(t, model.Data.Entry)
	})
	t.Run("plural", func(t *testing.T) {
		resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, fmt.Sprintf(
			"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=60&minutesAfter=120", combinedStopID))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		check(t, findArrivalForTrip(t, model.Data.Entry.ArrivalsAndDepartures, combinedTripID))
	})
}

func TestBuildTripStatus_StalePositionRecordControls(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	now := serviceDate.Add(8*time.Hour + 30*time.Minute)
	tests := []struct {
		name       string
		record     bool
		canceled   bool
		recordAge  time.Duration
		wantStatus string
		wantPhase  string
	}{
		{name: "without a record stays default", wantStatus: "default", wantPhase: "scheduled"},
		{name: "expired record stays default", record: true, recordAge: 10 * time.Minute, wantStatus: "default", wantPhase: "scheduled"},
		{name: "fresh cancellation remains canceled", record: true, canceled: true, wantStatus: "CANCELED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithClock(t, clock.NewMockClock(now))
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			_, _, tripID, _ := setupDelayPropTestData(t, api, 1)
			addDownstreamStop(t, api, tripID)
			gpsTime := now.Add(-16 * time.Minute)
			api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &gpsTime})
			if tt.record {
				recordTime := now.Add(-tt.recordAge)
				update := gtfs.Trip{ID: gtfs.TripID{ID: tripID}, Timestamp: &recordTime, Vehicle: &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}}}
				if tt.canceled {
					update.ID.ScheduleRelationship = gtfsrt.TripDescriptor_CANCELED
				}
				api.GtfsManager.MockAddFullTripUpdate(update)
			}
			status, extras, err := api.BuildTripStatus(context.Background(), "dp-agency", tripID, nil, serviceDate, now, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, status.Status)
			assert.Equal(t, tt.wantPhase, status.Phase)
			assert.Equal(t, tt.canceled, extras.record.present)
			assert.Equal(t, tt.canceled, status.Predicted)
			assert.Nil(t, status.LastKnownLocation)
			if tt.canceled {
				assert.Empty(t, status.NextStop)
				assert.Empty(t, status.ClosestStop)
				assert.Zero(t, status.DistanceAlongTrip)
				assert.Equal(t, now.UnixMilli(), status.LastUpdateTime.UnixMilli())
			} else {
				assert.True(t, status.LastUpdateTime.IsZero())
			}
		})
	}
}

func TestSingularArrival_RealBusReportsRecordTime(t *testing.T) {
	serviceDate := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	recordTime := serviceDate.Add(8*time.Hour + 30*time.Minute)
	positionTime := recordTime.Add(-20 * time.Second)
	api := createTestApiWithClock(t, clock.NewMockClock(recordTime))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	_, combinedStopID, tripID, _ := setupDelayPropTestData(t, api, 1)
	api.GtfsManager.MockAddVehicleWithOptions("bus", tripID, "dp-route", internalgtfs.MockVehicleOptions{Timestamp: &positionTime})
	api.GtfsManager.MockAddFullTripUpdate(gtfs.Trip{
		ID:        gtfs.TripID{ID: tripID},
		Timestamp: &recordTime,
		Vehicle:   &gtfs.Vehicle{ID: &gtfs.VehicleID{ID: "bus"}},
	})

	resp, body := serveApiAndRetrieveEndpoint(t, api, fmt.Sprintf(
		"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d&stopSequence=1",
		combinedStopID, utils.FormCombinedID("dp-agency", tripID), serviceDate.UnixMilli()))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	entry := body.Data.(map[string]any)["entry"].(map[string]any)
	assert.EqualValues(t, recordTime.UnixMilli(), entry["lastUpdateTime"])
}
