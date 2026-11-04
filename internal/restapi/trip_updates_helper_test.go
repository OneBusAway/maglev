package restapi

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// devDate is a placeholder service date for tests that don't exercise the
// Time-based deviation path. Trip IDs in these mocks don't exist in the
// static DB, so the absolute-time selection logic falls back to the
// pickFirstAvailableSTUDelay branch (first STU with a Delay, forward order).
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
	assert.Equal(t, 60, deviation, "delay-only update with no DB schedule falls through to pickFirstAvailableSTUDelay")
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

// TestGetScheduleDeviation_BlockNotActiveDoesNotFallThroughToSTU regresses
// the bug where blockNotActive returning (0,false) caused the caller to
// fall through to pickClosestSTUDeviation, surfacing per-stop delays from
// a record Java would have discarded.
func TestGetScheduleDeviation_BlockNotActiveDoesNotFallThroughToSTU(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// Bogus trip-level delay alongside a sane per-STU delay: the STU path
	// must NOT recover the per-stop value when blockNotActive has fired.
	bogus := 41 * time.Hour
	stop := "stop-A"
	stuDelay := 130 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stop, Arrival: &gtfs.StopTimeEvent{Delay: &stuDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-bogus-with-stu", &bogus, updates)

	deviation, hasData := api.GetScheduleDeviationForBlock(
		context.Background(), []string{"trip-bogus-with-stu"}, devDate, devNow,
	)
	assert.Equal(t, 0, deviation,
		"blockNotActive must discard the entire record — not fall through to STU")
	assert.False(t, hasData,
		"hasData=false signals the caller to skip the deviation shift entirely")
}

// TestGetScheduleDeviation_FallbackPicksFreshestSTU covers the Tier-3
// fallback (pickFirstAvailableSTUDelay) — the path that fires when the
// static schedule for a trip isn't in the DB and Java's closest-in-time
// picker can't run. A bus is currently 10 min late at its next stop, mid
// stop is 5 min late, terminal has absorbed the delay to 0s (recovery
// time built into the last leg). The right answer is 600s (freshest,
// closest to now); the terminal-STU-first behavior returned 0s and made
// tripStatus report "on time" while the bus was 10 minutes late.
func TestGetScheduleDeviation_FallbackPicksFreshestSTU(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	nextStop, midStop, endStop := "stop-next", "stop-mid", "stop-terminal"
	nextDelay := 600 * time.Second
	midDelay := 300 * time.Second
	endDelay := 0 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &nextStop, Arrival: &gtfs.StopTimeEvent{Delay: &nextDelay}},
		{StopID: &midStop, Arrival: &gtfs.StopTimeEvent{Delay: &midDelay}},
		{StopID: &endStop, Arrival: &gtfs.StopTimeEvent{Delay: &endDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-recovery-time-fallback", nil, updates)

	deviation, hasData := api.GetScheduleDeviationForBlock(
		context.Background(), []string{"trip-recovery-time-fallback"}, devDate, devNow,
	)
	assert.True(t, hasData)
	assert.Equal(t, 600, deviation,
		"fallback must return the freshest (first) STU's delay, not the terminal's 0s")
}

// TestGetScheduleDeviation_FallbackForwardWalkAcrossBlockTrips confirms the
// walk crosses block-trip boundaries in forward order — the first STU with
// a delay wins, even if it's in the last of several block trips (unusual,
// but the previous reverse-walk would have picked a terminal STU regardless).
func TestGetScheduleDeviation_FallbackForwardWalkAcrossBlockTrips(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	firstTripStop := "first-stop"
	firstDelay := 42 * time.Second
	firstUpdates := []gtfs.StopTimeUpdate{
		{StopID: &firstTripStop, Arrival: &gtfs.StopTimeEvent{Delay: &firstDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-block-forward-first", nil, firstUpdates)

	secondTripStop := "second-stop"
	secondDelay := 999 * time.Second
	secondUpdates := []gtfs.StopTimeUpdate{
		{StopID: &secondTripStop, Arrival: &gtfs.StopTimeEvent{Delay: &secondDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-block-forward-second", nil, secondUpdates)

	deviation, hasData := api.GetScheduleDeviationForBlock(
		context.Background(),
		[]string{"trip-block-forward-first", "trip-block-forward-second"},
		devDate, devNow,
	)
	assert.True(t, hasData)
	assert.Equal(t, 42, deviation,
		"outer walk must be forward — first block trip's STU wins over later trips'")
}

func TestGetStopDelaysFromTripUpdates_NoUpdates(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	delays := api.GetStopDelaysFromTripUpdates("no-such-trip", nil)
	assert.Equal(t, 0, delays.Len())
}

func TestGetStopDelaysFromTripUpdates_WithArrivalDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-A"
	futureTime := devNow.Add(30 * time.Minute)
	arrivalDelay := 45 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Time: &futureTime, Delay: &arrivalDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-stop-delays-arrival", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-stop-delays-arrival", nil)
	assert.Equal(t, 1, delays.Len())
	assert.Equal(t, int64(45), delayFor(delays, "stop-A").ArrivalDelay)
	assert.Equal(t, int64(0), delayFor(delays, "stop-A").DepartureDelay)
}

func TestGetStopDelaysFromTripUpdates_WithDepartureDelay(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-B"
	futureTime := devNow.Add(30 * time.Minute)
	departureDelay := 75 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Departure: &gtfs.StopTimeEvent{Time: &futureTime, Delay: &departureDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-stop-delays-departure", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-stop-delays-departure", nil)
	assert.Equal(t, 1, delays.Len())
	assert.Equal(t, int64(0), delayFor(delays, "stop-B").ArrivalDelay)
	assert.Equal(t, int64(75), delayFor(delays, "stop-B").DepartureDelay)
}

func TestGetStopDelaysFromTripUpdates_SkipsStopWithNoStopIDOrSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	futureTime := devNow.Add(30 * time.Minute)
	arrivalDelay := 30 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: nil, Arrival: &gtfs.StopTimeEvent{Time: &futureTime, Delay: &arrivalDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-nil-stopid", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-nil-stopid", nil)
	assert.Equal(t, 0, delays.Len(), "stop updates with neither StopID nor StopSequence should be skipped")
}

func TestGetStopDelaysFromTripUpdates_IncludesStopWithZeroDelays(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-C"
	futureTime := devNow.Add(30 * time.Minute)
	zeroDelay := time.Duration(0)
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Time: &futureTime, Delay: &zeroDelay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-zero-delays", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-zero-delays", nil)
	assert.Equal(t, 1, delays.Len(), "stops with zero delays should be included")
	_, hasC := delays.For("stop-C", 0)
	assert.True(t, hasC)
	assert.Equal(t, int64(0), delayFor(delays, "stop-C").ArrivalDelay)
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
	require.NoError(t, err, "GetStopTimesForTrip failed for %s: a real DB error must not be masked as a benign skip", trip.ID)
	if len(stopTimes) < 3 {
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
	futureTime := devNow.Add(30 * time.Minute)
	delayA := 30 * time.Second
	delayB := 60 * time.Second

	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopA, Arrival: &gtfs.StopTimeEvent{Time: &futureTime, Delay: &delayA}},
		{StopID: &stopB, Departure: &gtfs.StopTimeEvent{Delay: &delayB}},
		{StopID: &stopC, Arrival: &gtfs.StopTimeEvent{Time: &futureTime}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-multi-stops", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-multi-stops", nil)
	assert.Equal(t, 3, delays.Len(), "all stops with StopID should be included")
	assert.Equal(t, int64(30), delayFor(delays, "stop-A").ArrivalDelay)
	assert.Equal(t, int64(60), delayFor(delays, "stop-B").DepartureDelay)
	_, hasC := delays.For("stop-C", 0)
	assert.True(t, hasC)
	assert.Equal(t, int64(0), delayFor(delays, "stop-C").ArrivalDelay)
	assert.Equal(t, int64(0), delayFor(delays, "stop-C").DepartureDelay)
}

// delayFor reads a stop_id-keyed entry the way the pre-stop_sequence tests did.
func delayFor(d StopDelays, stopID string) StopDelayInfo {
	info, _ := d.For(stopID, 0)
	return info
}

func TestGetStopDelaysFromTripUpdates_LoopTripKeyedBySequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// A loop trip visits stop-A at sequence 1 and again at sequence 3. The
	// feed reports different delays for the two visits.
	stopA := "stop-A"
	stopB := "stop-B"
	seq1, seq2, seq3 := uint32(1), uint32(2), uint32(3)
	onTime := 0 * time.Second
	late := 600 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopSequence: &seq1, StopID: &stopA, Arrival: &gtfs.StopTimeEvent{Delay: &onTime}},
		{StopSequence: &seq2, StopID: &stopB, Arrival: &gtfs.StopTimeEvent{Delay: &onTime}},
		{StopSequence: &seq3, StopID: &stopA, Arrival: &gtfs.StopTimeEvent{Delay: &late}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-loop", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-loop", nil)

	first, ok := delays.For("stop-A", 1)
	assert.True(t, ok)
	assert.Equal(t, int64(0), first.ArrivalDelay, "sequence 1 keeps its own delay")

	second, ok := delays.For("stop-A", 3)
	assert.True(t, ok)
	assert.Equal(t, int64(600), second.ArrivalDelay, "sequence 3 must not collapse into sequence 1")
}

func TestGetStopDelaysFromTripUpdates_SequenceOnlyUpdateIsKept(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// GTFS-RT allows a StopTimeUpdate to carry stop_sequence with no stop_id.
	seq := uint32(4)
	delay := 120 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopSequence: &seq, StopID: nil, Departure: &gtfs.StopTimeEvent{Delay: &delay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-seq-only", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-seq-only", nil)
	info, ok := delays.For("any-stop", 4)
	assert.True(t, ok, "an update with stop_sequence but no stop_id must not be dropped")
	assert.Equal(t, int64(120), info.DepartureDelay)
}

func TestGetStopDelaysFromTripUpdates_FallsBackToStopIDWithoutSequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopID := "stop-C"
	delay := 30 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopID: &stopID, Arrival: &gtfs.StopTimeEvent{Delay: &delay}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-id-only", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-id-only", nil)
	// The scheduled stop-time has a sequence the feed never mentioned, so the
	// lookup must fall through to stop_id.
	info, ok := delays.For("stop-C", 7)
	assert.True(t, ok)
	assert.Equal(t, int64(30), info.ArrivalDelay)
}

// TestGetScheduleDeviation_SequenceOnlySTUReachesScheduleMatch regresses the
// closest-in-time picker dropping updates that carry stop_sequence but no
// stop_id. The schedule lookup is keyed by stop_id, so such an update
// contributed a zero scheduled time and picker.consider discarded it.
//
// The update sets Arrival.Time rather than Arrival.Delay on purpose: the
// Tier-3 pickFirstAvailableSTUDelay fallback only reads Delay, so with Time
// the assertion can only be satisfied by the schedule-matching path.
func TestGetScheduleDeviation_SequenceOnlySTUReachesScheduleMatch(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	ctx := context.Background()

	var tripID string
	var stopSequence, arrivalNanos int64
	err := api.GtfsManager.GtfsDB.DB.QueryRowContext(ctx,
		`SELECT trip_id, stop_sequence, arrival_time FROM stop_times WHERE arrival_time > 0 LIMIT 1`,
	).Scan(&tripID, &stopSequence, &arrivalNanos)
	require.NoError(t, err, "test data should contain at least one scheduled stop time")

	scheduledSeconds := arrivalNanos / int64(time.Second)
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	const lateBySeconds = 120

	seq := uint32(stopSequence)
	predictedArrival := serviceDate.Add(time.Duration(scheduledSeconds+lateBySeconds) * time.Second)
	updates := []gtfs.StopTimeUpdate{
		{StopSequence: &seq, StopID: nil, Arrival: &gtfs.StopTimeEvent{Time: &predictedArrival}},
	}
	api.GtfsManager.MockAddTripUpdate(tripID, nil, updates)

	currentTime := serviceDate.Add(time.Duration(scheduledSeconds) * time.Second)
	deviation, hasData := api.GetScheduleDeviationForBlock(ctx, []string{tripID}, serviceDate, currentTime)

	assert.True(t, hasData, "an update with stop_sequence but no stop_id must still reach schedule matching")
	assert.Equal(t, lateBySeconds, deviation)
}

// TestGetStopDelaysFromTripUpdates_SequencedUpdateDoesNotSeedStopIDFallback
// pins that an update carrying a stop_sequence stays out of the stop_id
// fallback. A loop trip with an update only for the second visit must not
// serve that delay to a lookup for the first visit.
func TestGetStopDelaysFromTripUpdates_SequencedUpdateDoesNotSeedStopIDFallback(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	stopA := "stop-A"
	seq3 := uint32(3)
	late := 600 * time.Second
	updates := []gtfs.StopTimeUpdate{
		{StopSequence: &seq3, StopID: &stopA, Arrival: &gtfs.StopTimeEvent{Delay: &late}},
	}
	api.GtfsManager.MockAddTripUpdate("trip-seq-no-fallback", nil, updates)

	delays := api.GetStopDelaysFromTripUpdates("trip-seq-no-fallback", nil)

	_, ok := delays.For("stop-A", 1)
	assert.False(t, ok, "sequence 1 has no update and must not inherit sequence 3's delay")

	info, ok := delays.For("stop-A", 3)
	assert.True(t, ok)
	assert.Equal(t, int64(600), info.ArrivalDelay)
}

// TestGetStopDelaysFromTripUpdates_SequenceMustAgreeWithStopID covers feeds whose stop_sequence has drifted from the static schedule.
func TestGetStopDelaysFromTripUpdates_SequenceMustAgreeWithStopID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	stopA, stopB, stopC := "stop-A", "stop-B", "stop-C"
	seq0, seq1, seq2, seq101 := uint32(0), uint32(1), uint32(2), uint32(101)
	static := []*gtfsdb.StopTime{
		{StopID: stopA, StopSequence: 1},
		{StopID: stopB, StopSequence: 2},
		{StopID: stopC, StopSequence: 3},
	}

	tests := []struct {
		name    string
		updates []gtfs.StopTimeUpdate
	}{
		{
			name: "sequences off by one",
			updates: []gtfs.StopTimeUpdate{
				{StopSequence: &seq0, StopID: &stopA, Arrival: delayEvent(30 * time.Second)},
				{StopSequence: &seq1, StopID: &stopB, Arrival: delayEvent(120 * time.Second)},
				{StopSequence: &seq2, StopID: &stopC, Arrival: delayEvent(300 * time.Second)},
			},
		},
		{
			name: "sequence outside the schedule",
			updates: []gtfs.StopTimeUpdate{
				{StopSequence: &seq101, StopID: &stopB, Arrival: delayEvent(120 * time.Second)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)
			api.GtfsManager.MockAddTripUpdate("trip-drifted-sequence", nil, tt.updates)

			info, ok := api.GetStopDelaysFromTripUpdates("trip-drifted-sequence", static).For(stopB, 2)
			require.True(t, ok)
			assert.Equal(t, int64(120), info.ArrivalDelay)
		})
	}
}

func delayEvent(d time.Duration) *gtfs.StopTimeEvent {
	return &gtfs.StopTimeEvent{Delay: &d}
}

func TestDeviationAtScheduledArrival(t *testing.T) {
	samples := []deviationSample{
		{scheduledArrival: 100, deviation: 10},
		{scheduledArrival: 200, deviation: 20},
	}

	tests := []struct {
		name             string
		scheduledArrival int64
		wantDeviation    int
		wantPropagates   bool
	}{
		{name: "upstream of every sample", scheduledArrival: 50, wantPropagates: false},
		{name: "exact sample", scheduledArrival: 100, wantDeviation: 10, wantPropagates: true},
		{name: "between samples takes the previous one", scheduledArrival: 150, wantDeviation: 10, wantPropagates: true},
		{name: "downstream of every sample takes the last one", scheduledArrival: 300, wantDeviation: 20, wantPropagates: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deviation, propagates := deviationAtScheduledArrival(samples, tt.scheduledArrival)
			assert.Equal(t, tt.wantPropagates, propagates)
			assert.Equal(t, tt.wantDeviation, deviation)
		})
	}
}

func TestBlockDeviationSamples(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	trip := mustGetTrip(t, api)
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
	require.NoError(t, err)
	// Samples are keyed by scheduled arrival, so use stops whose arrivals
	// differ; consecutive stops can share one.
	stopTimes = slices.CompactFunc(stopTimes, func(a, b gtfsdb.StopTime) bool { return a.ArrivalTime == b.ArrivalTime })
	require.GreaterOrEqual(t, len(stopTimes), 3, "need a trip with at least 3 distinct stop arrival times")

	stuFor := func(st gtfsdb.StopTime, arrivalDelay, departureDelay *time.Duration) gtfs.StopTimeUpdate {
		seq := uint32(st.StopSequence)
		stu := gtfs.StopTimeUpdate{StopID: &st.StopID, StopSequence: &seq}
		if arrivalDelay != nil {
			stu.Arrival = &gtfs.StopTimeEvent{Delay: arrivalDelay}
		}
		if departureDelay != nil {
			stu.Departure = &gtfs.StopTimeEvent{Delay: departureDelay}
		}
		return stu
	}
	arrivalOnly, arrivalDelay, departureDelay, skippedDelay := 60*time.Second, 30*time.Second, 90*time.Second, 500*time.Second
	skipped := stuFor(stopTimes[2], &skippedDelay, nil)
	skipped.ScheduleRelationship = gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED
	api.GtfsManager.MockAddTripUpdate(trip.ID, nil, []gtfs.StopTimeUpdate{
		stuFor(stopTimes[0], &arrivalOnly, nil),
		stuFor(stopTimes[1], &arrivalDelay, &departureDelay),
		skipped,
	})

	samples := api.blockDeviationSamples(ctx, []string{trip.ID}, devDate, devNow)

	assert.Equal(t, []deviationSample{
		{scheduledArrival: utils.NanosToSeconds(stopTimes[0].ArrivalTime), deviation: 60},
		{scheduledArrival: utils.NanosToSeconds(stopTimes[1].ArrivalTime), deviation: 90},
	}, samples, "a departure prediction wins over an arrival one, and skipped stops contribute no sample")
}

func TestDeviationSampleCache(t *testing.T) {
	serviceMidnight := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	nextServiceMidnight := serviceMidnight.AddDate(0, 0, 1)
	countingCompute := func(calls *int) func() []deviationSample {
		return func() []deviationSample {
			*calls++
			return []deviationSample{{scheduledArrival: 100, deviation: 60}}
		}
	}

	tests := []struct {
		name      string
		cache     deviationSampleCache
		lookups   [][]string
		midnights []time.Time
		wantCalls int
	}{
		{
			name:      "same block and service date computes once",
			cache:     deviationSampleCache{},
			lookups:   [][]string{{"A", "B"}, {"A", "B"}},
			midnights: []time.Time{serviceMidnight, serviceMidnight},
			wantCalls: 1,
		},
		{
			name:      "different blocks compute separately",
			cache:     deviationSampleCache{},
			lookups:   [][]string{{"A", "B"}, {"C"}},
			midnights: []time.Time{serviceMidnight, serviceMidnight},
			wantCalls: 2,
		},
		{
			name:      "different service dates compute separately",
			cache:     deviationSampleCache{},
			lookups:   [][]string{{"A", "B"}, {"A", "B"}},
			midnights: []time.Time{serviceMidnight, nextServiceMidnight},
			wantCalls: 2,
		},
		{
			name:      "nil cache always computes",
			cache:     nil,
			lookups:   [][]string{{"A", "B"}, {"A", "B"}},
			midnights: []time.Time{serviceMidnight, serviceMidnight},
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			for i, blockTripIDs := range tt.lookups {
				samples := tt.cache.samplesFor(blockTripIDs, tt.midnights[i], countingCompute(&calls))
				assert.Equal(t, []deviationSample{{scheduledArrival: 100, deviation: 60}}, samples)
			}
			assert.Equal(t, tt.wantCalls, calls)
		})
	}
}

func TestMatchScheduleEntry(t *testing.T) {
	seq1, seq2, seq99 := uint32(1), uint32(2), uint32(99)
	entry1 := schedEntry{seq: 1, arr: 1000, dep: 1060}
	entry2 := schedEntry{seq: 2, arr: 5000, dep: 5060}
	entry3 := schedEntry{seq: 3, arr: 9000, dep: 9060}

	// Fixtures where arrival delta and departure delta disagree on which entry is closest.
	arrDisagreesEntry1 := schedEntry{seq: 1, arr: 1000, dep: 1060}
	arrDisagreesEntry5 := schedEntry{seq: 5, arr: 3000, dep: 5000}

	depDisagreesEntry1 := schedEntry{seq: 1, arr: 3000, dep: 3060}
	depDisagreesEntry5 := schedEntry{seq: 5, arr: 1000, dep: 3950}

	// Tiebreak fixture where two entries have identical minimum absolute delta.
	tieEntry1 := schedEntry{seq: 1, arr: 1000, dep: 1060}
	tieEntry2 := schedEntry{seq: 2, arr: 2000, dep: 2060}

	tests := []struct {
		name          string
		entries       []schedEntry
		stuSeq        *uint32
		stuRefSeconds int64
		wantArr       int64
		wantDep       int64
	}{
		{
			name:    "empty entries returns zeroes",
			entries: nil,
			wantArr: 0,
			wantDep: 0,
		},
		{
			name:    "sequence match prefers matching sequence",
			entries: []schedEntry{entry1, entry2, entry3},
			stuSeq:  &seq2,
			wantArr: entry2.arr,
			wantDep: entry2.dep,
		},
		{
			name:          "single entry falls back to that entry",
			entries:       []schedEntry{entry1},
			stuRefSeconds: 8000,
			wantArr:       entry1.arr,
			wantDep:       entry1.dep,
		},
		{
			name:          "zero ref seconds falls back to first entry even when later entry is closer to zero",
			entries:       []schedEntry{entry3, entry2, entry1},
			stuRefSeconds: 0,
			wantArr:       entry3.arr,
			wantDep:       entry3.dep,
		},
		{
			name:          "negative ref seconds falls back to first entry even when later entry is closer to zero",
			entries:       []schedEntry{entry3, entry2, entry1},
			stuRefSeconds: -100,
			wantArr:       entry3.arr,
			wantDep:       entry3.dep,
		},
		{
			name:          "loop trip matches closest entry by arrival when departure delta disagrees",
			entries:       []schedEntry{arrDisagreesEntry1, arrDisagreesEntry5},
			stuRefSeconds: 3010,
			wantArr:       arrDisagreesEntry5.arr,
			wantDep:       arrDisagreesEntry5.dep,
		},
		{
			name:          "loop trip matches closest entry by departure when arrival delta disagrees",
			entries:       []schedEntry{depDisagreesEntry1, depDisagreesEntry5},
			stuRefSeconds: 3940,
			wantArr:       depDisagreesEntry5.arr,
			wantDep:       depDisagreesEntry5.dep,
		},
		{
			name:          "tie in closest proximity keeps first entry",
			entries:       []schedEntry{tieEntry1, tieEntry2},
			stuRefSeconds: 1530, // tieEntry1 dep delta 470, tieEntry2 arr delta 470
			wantArr:       tieEntry1.arr,
			wantDep:       tieEntry1.dep,
		},
		{
			name:          "sequence provided but missing falls back to closest reference time",
			entries:       []schedEntry{entry1, entry2, entry3},
			stuSeq:        &seq99,
			stuRefSeconds: 8900, // closest to entry3
			wantArr:       entry3.arr,
			wantDep:       entry3.dep,
		},
		{
			name:    "sequence match on first entry",
			entries: []schedEntry{entry1, entry2, entry3},
			stuSeq:  &seq1,
			wantArr: entry1.arr,
			wantDep: entry1.dep,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArr, gotDep := matchScheduleEntry(tt.entries, tt.stuSeq, tt.stuRefSeconds)
			assert.Equal(t, tt.wantArr, gotArr)
			assert.Equal(t, tt.wantDep, gotDep)
		})
	}
}

func TestMatchScheduleEntryBySequence(t *testing.T) {
	entryMap := map[string][]schedEntry{
		"stop-1": {{seq: 1, arr: 100, dep: 120}},
		"stop-2": {{seq: 2, arr: 200, dep: 220}, {seq: 4, arr: 400, dep: 420}},
	}

	tests := []struct {
		name    string
		entries map[string][]schedEntry
		stuSeq  uint32
		wantArr int64
		wantDep int64
	}{
		{
			name:    "empty map returns zeroes",
			entries: nil,
			stuSeq:  1,
			wantArr: 0,
			wantDep: 0,
		},
		{
			name:    "missing sequence returns zeroes",
			entries: entryMap,
			stuSeq:  99,
			wantArr: 0,
			wantDep: 0,
		},
		{
			name:    "matching sequence returns arrival and departure",
			entries: entryMap,
			stuSeq:  4,
			wantArr: 400,
			wantDep: 420,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArr, gotDep := matchScheduleEntryBySequence(tt.entries, tt.stuSeq)
			assert.Equal(t, tt.wantArr, gotArr)
			assert.Equal(t, tt.wantDep, gotDep)
		})
	}
}

func TestMinAbs(t *testing.T) {
	tests := []struct {
		name string
		a    int64
		b    int64
		want int64
	}{
		{
			name: "negative and positive picks smaller absolute value",
			a:    -10,
			b:    20,
			want: 10,
		},
		{
			name: "positive and negative picks smaller absolute value",
			a:    50,
			b:    -5,
			want: 5,
		},
		{
			name: "both negative picks smaller absolute value",
			a:    -30,
			b:    -40,
			want: 30,
		},
		{
			name: "zero and negative",
			a:    0,
			b:    -15,
			want: 0,
		},
		{
			name: "equal absolute values with opposing signs",
			a:    -25,
			b:    25,
			want: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, minAbs(tt.a, tt.b))
		})
	}
}

func TestStuReferenceTime(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := serviceDate.Add(3600 * time.Second) // 1:00 AM (3600 seconds)
	arrTime := serviceDate.Add(4000 * time.Second)
	depTime := serviceDate.Add(4200 * time.Second)
	delay60 := 60 * time.Second
	delay120 := 120 * time.Second

	tests := []struct {
		name string
		stu  gtfs.StopTimeUpdate
		want int64
	}{
		{
			name: "arrival time preferred over everything",
			stu: gtfs.StopTimeUpdate{
				Arrival:   &gtfs.StopTimeEvent{Time: &arrTime, Delay: &delay60},
				Departure: &gtfs.StopTimeEvent{Time: &depTime, Delay: &delay120},
			},
			want: 4000,
		},
		{
			name: "arrival delay used when arrival time nil",
			stu: gtfs.StopTimeUpdate{
				Arrival:   &gtfs.StopTimeEvent{Delay: &delay60},
				Departure: &gtfs.StopTimeEvent{Time: &depTime},
			},
			want: 3600 - 60, // 3540
		},
		{
			name: "departure time used when arrival nil",
			stu: gtfs.StopTimeUpdate{
				Departure: &gtfs.StopTimeEvent{Time: &depTime},
			},
			want: 4200,
		},
		{
			name: "departure time preferred over departure delay when arrival nil",
			stu: gtfs.StopTimeUpdate{
				Departure: &gtfs.StopTimeEvent{
					Time:  &depTime,
					Delay: &delay120,
				},
			},
			want: 4200,
		},
		{
			name: "departure delay used when arrival nil and departure time nil",
			stu: gtfs.StopTimeUpdate{
				Departure: &gtfs.StopTimeEvent{Delay: &delay120},
			},
			want: 3600 - 120, // 3480
		},
		{
			name: "empty update returns -1",
			stu:  gtfs.StopTimeUpdate{},
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stuReferenceTime(tt.stu, serviceDate, currentTime)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStuPredictedFromEvent(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	eventTime := serviceDate.Add(5000 * time.Second)
	delay := 150 * time.Second

	tests := []struct {
		name  string
		event *gtfs.StopTimeEvent
		sched int64
		want  int64
	}{
		{
			name:  "nil event returns 0",
			event: nil,
			sched: 1000,
			want:  0,
		},
		{
			name:  "event with time returns seconds since service date",
			event: &gtfs.StopTimeEvent{Time: &eventTime},
			sched: 1000,
			want:  5000,
		},
		{
			name: "event with both time and delay prefers time",
			event: &gtfs.StopTimeEvent{
				Time:  &eventTime,
				Delay: &delay,
			},
			sched: 4000,
			want:  5000,
		},
		{
			name:  "event with delay and positive sched adds delay to sched",
			event: &gtfs.StopTimeEvent{Delay: &delay},
			sched: 4000,
			want:  4150,
		},
		{
			name:  "event with delay and zero sched returns 0",
			event: &gtfs.StopTimeEvent{Delay: &delay},
			sched: 0,
			want:  0,
		},
		{
			name:  "event without time or delay returns 0",
			event: &gtfs.StopTimeEvent{},
			sched: 1000,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stuPredictedFromEvent(tt.event, tt.sched, serviceDate)
			assert.Equal(t, tt.want, got)
		})
	}
}
