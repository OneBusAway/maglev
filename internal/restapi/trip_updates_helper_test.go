package restapi

import (
	"context"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

// devDate is a placeholder service date for tests that don't exercise the
// Time-based deviation path. Trip IDs in these mocks don't exist in the
// static DB, so the absolute-time selection logic falls back to the simple
// "reverse-walk for the latest Delay" branch.
var devDate = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
var devNow = devDate.Add(12 * time.Hour) // arbitrary currentTime

func TestGetScheduleDeviation_NoUpdates(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"no-such-trip"}, devDate, devNow)
	assert.Equal(t, 0, deviation)
	assert.False(t, hasData, "no trip updates should return hasData=false")
}

// TestGetScheduleDeviation_TripLevelDelayWins: per Java's applyTripUpdatesToRecord,
// a trip-level `delay` short-circuits the per-stop selection — it is the schedule
// deviation, no further processing.
func TestGetScheduleDeviation_TripLevelDelayWins(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	tripDelay := 30 * time.Second
	stopID := "stop-1"
	stopDelay := 90 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Delay: &stopDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-precedence-test", &tripDelay, updates)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-precedence-test"}, devDate, devNow)
	assert.Equal(t, 30, deviation, "trip-level Delay wins immediately (Java's tripUpdateHasDelay short-circuit)")
	assert.True(t, hasData)
}

func TestGetScheduleDeviation_TripLevelDelayWithoutStopUpdates(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	delay := 90 * time.Second
	api.GtfsManager.MockAddTripUpdate("trip-delay-test", &delay, nil)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-delay-test"}, devDate, devNow)
	assert.Equal(t, 90, deviation)
	assert.True(t, hasData)
}

func TestGetScheduleDeviation_StopLevelArrivalDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-1"
	arrivalDelay := 60 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Delay: &arrivalDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-arrival-test", nil, updates)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-arrival-test"}, devDate, devNow)
	assert.Equal(t, 60, deviation, "delay-only update with no DB schedule falls through to reverse-walk fallback")
	assert.True(t, hasData)
}

func TestGetScheduleDeviation_StopLevelDepartureDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-1"
	departureDelay := 120 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Departure: &gtfs.StopTimeEvent{Delay: &departureDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-departure-test", nil, updates)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-departure-test"}, devDate, devNow)
	assert.Equal(t, 120, deviation)
	assert.True(t, hasData)
}

func TestGetScheduleDeviation_StopUpdateWithNoDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-1"
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-nodelay-test", nil, updates)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-nodelay-test"}, devDate, devNow)
	assert.Equal(t, 0, deviation)
	assert.False(t, hasData, "trip update with no delay data should report hasData=false")
}

func TestGetScheduleDeviation_ZeroDeviationIsDistinguishedFromNoData(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	zeroDelay := time.Duration(0)
	api.GtfsManager.MockAddTripUpdate("trip-zero-delay", &zeroDelay, nil)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-zero-delay"}, devDate, devNow)
	assert.Equal(t, 0, deviation)
	assert.True(t, hasData, "zero delay with trip update should still report hasData=true")

	deviation2, hasData2 := api.GetScheduleDeviationForBlock(context.Background(), []string{"nonexistent-trip"}, devDate, devNow)
	assert.Equal(t, 0, deviation2)
	assert.False(t, hasData2, "nonexistent trip should report hasData=false")
}

func TestGetScheduleDeviation_BlockNotActiveDiscardsBogusDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// 168260s = 46h44m20s — well over Java's 1-hour threshold.
	bogus := 168260 * time.Second
	api.GtfsManager.MockAddTripUpdate("trip-bogus-delay", &bogus, nil)

	deviation, hasData := api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-bogus-delay"}, devDate, devNow)
	assert.Equal(t, 0, deviation,
		"bogus publisher delay must not propagate")
	assert.False(t, hasData,
		"|delay| > 1 hour → discard VehicleLocationRecord (Java's blockNotActive); caller falls back to schedule-only")

	// And the symmetric negative case.
	negBogus := -3700 * time.Second
	api.GtfsManager.MockAddTripUpdate("trip-bogus-negative", &negBogus, nil)
	deviation, hasData = api.GetScheduleDeviationForBlock(context.Background(), []string{"trip-bogus-negative"}, devDate, devNow)
	assert.Equal(t, 0, deviation)
	assert.False(t, hasData, "negative delays beyond -1h must also be discarded")
}

func TestGetStopDelaysFromTripUpdates_NoUpdates(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	delays := api.GetStopDelaysFromTripUpdates("no-such-trip")
	assert.Empty(t, delays)
}

func TestGetStopDelaysFromTripUpdates_WithArrivalDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-A"
	arrivalDelay := 45 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Delay: &arrivalDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-stop-delays-arrival", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-stop-delays-arrival")
	assert.Len(t, delays, 1)
	assert.Equal(t, int64(45), delays["stop-A"].ArrivalDelay)
	assert.Equal(t, int64(0), delays["stop-A"].DepartureDelay)
}

func TestGetStopDelaysFromTripUpdates_WithDepartureDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-B"
	departureDelay := 75 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Departure: &gtfs.StopTimeEvent{Delay: &departureDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-stop-delays-departure", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-stop-delays-departure")
	assert.Len(t, delays, 1)
	assert.Equal(t, int64(0), delays["stop-B"].ArrivalDelay)
	assert.Equal(t, int64(75), delays["stop-B"].DepartureDelay)
}

func TestGetStopDelaysFromTripUpdates_SkipsStopWithNoStopID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	arrivalDelay := 30 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: nil, Arrival: &gtfs.StopTimeEvent{Delay: &arrivalDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-nil-stopid", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-nil-stopid")
	assert.Empty(t, delays, "stop updates without StopID should be skipped")
}

func TestGetStopDelaysFromTripUpdates_IncludesStopWithZeroDelays(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-C"
	zeroDelay := time.Duration(0)
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Delay: &zeroDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-zero-delays", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-zero-delays")
	assert.Len(t, delays, 1, "stops with zero delays should be included")
	assert.Contains(t, delays, "stop-C")
	assert.Equal(t, int64(0), delays["stop-C"].ArrivalDelay)
}

// TestGetScheduleDeviationForBlock_ClosestInTimeAgainstRealSchedule
// exercises the path that the other deviation tests can't: when the trip
// IS in the static DB, loadScheduled returns real arrival/departure
// seconds and the closest-in-time-against-scheduled branch fires
// (rather than the reverse-walk fallback). A "decoy" STU 9999 s late at
// a far-from-currentTime stop must NOT win against the nearer 17 s
// candidate.
func TestGetScheduleDeviationForBlock_ClosestInTimeAgainstRealSchedule(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	trip := mustGetTrip(t, api)
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
	if err != nil || len(stopTimes) < 3 {
		t.Skip("need a real trip with >= 3 stop_times for this path")
	}

	// nearStop ≈ at currentTime (~12h into the service day in our default
	// devNow); farStop is the trip's first stop (typically early morning).
	nearStop := stopTimes[len(stopTimes)/2].StopID
	farStop := stopTimes[0].StopID
	nearDelay := 17 * time.Second
	farDelay := 9999 * time.Second // intentionally absurd; must NOT win

	api.GtfsManager.MockAddTripUpdate(trip.ID, nil, []gtfs.StopTimeUpdate{
		{StopID: &farStop, Arrival: &gtfs.StopTimeEvent{Delay: &farDelay}},
		{StopID: &nearStop, Arrival: &gtfs.StopTimeEvent{Delay: &nearDelay}},
	})

	// Align currentTime to the near stop's scheduled arrival so delta=0
	// at that candidate.
	loc := time.UTC
	if z, _ := time.LoadLocation("America/Los_Angeles"); z != nil {
		loc = z
	}
	serviceDate := time.Date(2024, 11, 4, 0, 0, 0, 0, loc)
	var nearScheduledSec int64
	for _, st := range stopTimes {
		if st.StopID == nearStop {
			nearScheduledSec = st.ArrivalTime / int64(time.Second)
		}
	}
	currentTime := serviceDate.Add(time.Duration(nearScheduledSec) * time.Second)

	dev, hasData := api.GetScheduleDeviationForBlock(ctx, []string{trip.ID}, serviceDate, currentTime)
	assert.True(t, hasData)
	assert.Equal(t, 17, dev,
		"closest-in-time STU must win; the absurd 9999s decoy at a far stop must not")
}

func TestGetStopDelaysFromTripUpdates_MultipleStops(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopA := "stop-A"
	stopB := "stop-B"
	stopC := "stop-C"
	delayA := 30 * time.Second
	delayB := 60 * time.Second

	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopA, Arrival: &gtfs.StopTimeEvent{Delay: &delayA}},
		{StopID: &stopB, Departure: &gtfs.StopTimeEvent{Delay: &delayB}},
		{StopID: &stopC},
	}
	api.GtfsManager.MockAddTripUpdate("trip-multi-stops", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-multi-stops")
	assert.Len(t, delays, 3, "all stops with StopID should be included")
	assert.Equal(t, int64(30), delays["stop-A"].ArrivalDelay)
	assert.Equal(t, int64(60), delays["stop-B"].DepartureDelay)
	assert.Contains(t, delays, "stop-C")
	assert.Equal(t, int64(0), delays["stop-C"].ArrivalDelay)
	assert.Equal(t, int64(0), delays["stop-C"].DepartureDelay)
}
