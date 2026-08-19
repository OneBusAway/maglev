package gtfs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
)

// metricsTestNow is a fixed reference time (well within the "service-1"
// calendar's validity range) so active-trip-window assertions are
// deterministic regardless of when the test runs.
var metricsTestNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

// metricsTestNowSinceMidnight is metricsTestNow expressed as nanoseconds
// since its own midnight, matching the trips/block_layover schema-window
// convention.
var metricsTestNowSinceMidnight = metricsTestNow.Sub(
	time.Date(metricsTestNow.Year(), metricsTestNow.Month(), metricsTestNow.Day(), 0, 0, 0, 0, metricsTestNow.Location()))

// activeStopTimeUpdates returns a single stop_time_update whose predictions
// bracket the real wall clock, so isCombinedRecordActive/isTripActive treat
// it as currently active. Matched-trip activity is deliberately measured
// against time.Now(), not metricsTestNow (see populateRealtimeMetrics), so
// tests exercising matched counts must anchor predictions to real time too.
func activeStopTimeUpdates() []gtfs.StopTimeUpdate {
	arrival := time.Now().Add(-5 * time.Minute)
	departure := time.Now().Add(30 * time.Minute)
	return []gtfs.StopTimeUpdate{
		{Arrival: &gtfs.StopTimeEvent{Time: &arrival}, Departure: &gtfs.StopTimeEvent{Time: &departure}},
	}
}

// mustCreateCalendar inserts an every-day calendar service valid across 2024-2029.
func mustCreateCalendar(t *testing.T, manager *Manager, serviceID string) {
	t.Helper()
	_, err := manager.GtfsDB.Queries.CreateCalendar(context.Background(), gtfsdb.CreateCalendarParams{
		ID:        serviceID,
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20240101",
		EndDate:   "20291231",
	})
	require.NoError(t, err)
}

// defaultTestServiceID is the calendar service shared by mustCreateTrip and
// mustCreateInactiveTrip.
const defaultTestServiceID = "service-1"

// ensureDefaultCalendar creates defaultTestServiceID if it doesn't already
// exist, so tests can call mustCreateTrip/mustCreateInactiveTrip any number
// of times without colliding on the calendar row.
func ensureDefaultCalendar(t *testing.T, manager *Manager) {
	t.Helper()
	ctx := context.Background()
	if _, err := manager.GtfsDB.Queries.GetCalendarByServiceID(ctx, defaultTestServiceID); err != nil {
		mustCreateCalendar(t, manager, defaultTestServiceID)
	}
}

// mustCreateTrip inserts a static trip that is active all day, every day,
// so it counts as "currently active" for any `now` passed to GetMetrics.
// Creates its referenced calendar service if it doesn't already exist.
func mustCreateTrip(t *testing.T, manager *Manager, tripID, routeID string) {
	t.Helper()
	ensureDefaultCalendar(t, manager)

	_, err := manager.GtfsDB.Queries.CreateTrip(context.Background(), gtfsdb.CreateTripParams{
		ID:               tripID,
		RouteID:          routeID,
		ServiceID:        defaultTestServiceID,
		MinArrivalTime:   sql.NullInt64{Int64: 0, Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (24 * time.Hour).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)
}

// mustCreateInactiveTrip inserts a static trip whose schedule window never
// overlaps metricsTestNow, for tests that must confirm inactive trips are
// excluded from the active-trip count.
func mustCreateInactiveTrip(t *testing.T, manager *Manager, tripID, routeID string) {
	t.Helper()
	ensureDefaultCalendar(t, manager)

	// A short window early this morning, hours before metricsTestNow (12:00),
	// well outside the running-late/running-early tolerance.
	_, err := manager.GtfsDB.Queries.CreateTrip(context.Background(), gtfsdb.CreateTripParams{
		ID:               tripID,
		RouteID:          routeID,
		ServiceID:        defaultTestServiceID,
		MinArrivalTime:   sql.NullInt64{Int64: (1 * time.Hour).Nanoseconds(), Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (2 * time.Hour).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)
}

// mustCreateStop inserts a minimal static stop for metrics-matching tests.
func mustCreateStop(t *testing.T, manager *Manager, stopID string) {
	t.Helper()
	_, err := manager.GtfsDB.Queries.CreateStop(context.Background(), gtfsdb.CreateStopParams{
		ID:  stopID,
		Lat: 0,
		Lon: 0,
	})
	require.NoError(t, err)
}

func TestGetMetrics_NoRealtimeFeeds(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"RA": {Id: "RA", Agency: &gtfs.Agency{Id: "A"}},
		"RB": {Id: "RB", Agency: &gtfs.Agency{Id: "B"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "TA1", "RA")
	mustCreateTrip(t, manager, "TB1", "RB")

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"A", "B"}, snapshot.AgencyIDs)
	assert.Equal(t, 1, snapshot.ScheduledTripsCount["A"])
	assert.Equal(t, 1, snapshot.ScheduledTripsCount["B"])

	for _, agencyID := range []string{"A", "B"} {
		assert.Equal(t, 0, snapshot.RealtimeRecordsTotal[agencyID])
		assert.Equal(t, 0, snapshot.RealtimeTripCountsMatched[agencyID])
		assert.Equal(t, 0, snapshot.RealtimeTripCountsUnmatched[agencyID])
		assert.Equal(t, []string{}, snapshot.RealtimeTripIDsUnmatched[agencyID])
		assert.Equal(t, 0, snapshot.StopIDsMatchedCount[agencyID])
		assert.Equal(t, 0, snapshot.StopIDsUnmatchedCount[agencyID])
		assert.Equal(t, []string{}, snapshot.StopIDsUnmatched[agencyID])
		assert.Equal(t, int64(0), snapshot.TimeSinceLastRealtimeUpdate[agencyID])
	}
}

func TestGetMetrics_MatchedAndUnmatchedTrips(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1")

	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}, StopTimeUpdates: activeStopTimeUpdates()},
		{ID: gtfs.TripID{ID: "GHOST", RouteID: "R1"}},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 2, snapshot.RealtimeRecordsTotal["A"])
	assert.Equal(t, 1, snapshot.RealtimeTripCountsMatched["A"])
	assert.Equal(t, 1, snapshot.RealtimeTripCountsUnmatched["A"])
	assert.Equal(t, []string{"GHOST"}, snapshot.RealtimeTripIDsUnmatched["A"])
}

// TestGetMetrics_RecordsTotalGroupsTripUpdatesByBlock guards the fix for a
// discrepancy found by comparing Maglev's output against the production Java
// metrics.json: recordsTotal there is GtfsRealtimeSource#handleUpdates'
// combinedUpdates.size(), which groups trip updates by their static block
// (see groupTripsByBlock) — not by vehicle ID, and not one record per
// trip_update entity. A block's current trip and a look-ahead next-trip
// update on that same block are one record, not two, regardless of whether
// either entity carries a vehicle tag.
func TestGetMetrics_RecordsTotalGroupsTripUpdatesByBlock(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateCalendar(t, manager, "service-1")

	ctx := context.Background()
	_, err := manager.GtfsDB.Queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:               "CURRENT_LEG",
		RouteID:          "R1",
		ServiceID:        "service-1",
		BlockID:          sql.NullString{String: "BLOCK1", Valid: true},
		MinArrivalTime:   sql.NullInt64{Int64: 0, Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (24 * time.Hour).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)
	_, err = manager.GtfsDB.Queries.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID:               "NEXT_LEG",
		RouteID:          "R1",
		ServiceID:        "service-1",
		BlockID:          sql.NullString{String: "BLOCK1", Valid: true},
		MinArrivalTime:   sql.NullInt64{Int64: 0, Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (24 * time.Hour).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)

	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "CURRENT_LEG", RouteID: "R1"}, StopTimeUpdates: activeStopTimeUpdates()},
		{ID: gtfs.TripID{ID: "NEXT_LEG", RouteID: "R1"}},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.RealtimeRecordsTotal["A"],
		"both trip updates share BLOCK1, so they should collapse to one record")
	assert.Equal(t, 1, snapshot.RealtimeTripCountsMatched["A"],
		"matched counting follows the same block grouping as recordsTotal, so the block is matched once")
}

// TestGetMetrics_MatchedTripsRequireActivePrediction guards the fix for a
// second discrepancy found by comparing Maglev's output against production
// Java: matched counting there is further gated by
// GtfsRealtimeTripLibrary#isTripActive — a resolved record whose predicted
// stop times don't bracket "now" (already finished, or its first prediction
// more than an hour out) counts toward neither matched nor unmatched.
// Without this, Maglev counted every statically-resolvable trip ID as
// matched regardless of whether its predictions were current, running far
// higher than Java for the same feed.
func TestGetMetrics_MatchedTripsRequireActivePrediction(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1")

	longFinished := time.Now().Add(-2 * time.Hour)
	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{
			ID: gtfs.TripID{ID: "T1", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{
				{Arrival: &gtfs.StopTimeEvent{Time: &longFinished}, Departure: &gtfs.StopTimeEvent{Time: &longFinished}},
			},
		},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.RealtimeRecordsTotal["A"],
		"the record still exists and is resolvable, so it counts toward recordsTotal")
	assert.Equal(t, 0, snapshot.RealtimeTripCountsMatched["A"],
		"a resolved trip whose predictions already finished counts toward neither matched nor unmatched")
	assert.Equal(t, 0, snapshot.RealtimeTripCountsUnmatched["A"])
}

// TestGetMetrics_RecordsTotalTreatsBlocklessTripsAsStandaloneRecords covers
// the fallback side of the same fix: a trip update with no resolvable
// static block (unmatched entirely, or matched but genuinely blockless in
// GTFS) is its own record rather than merging with anything.
func TestGetMetrics_RecordsTotalTreatsBlocklessTripsAsStandaloneRecords(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1") // mustCreateTrip leaves block_id unset.
	mustCreateTrip(t, manager, "T2", "R1")

	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
		{ID: gtfs.TripID{ID: "T2", RouteID: "R1"}},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 2, snapshot.RealtimeRecordsTotal["A"],
		"neither trip has a static block, so each is its own record")
}

// TestGetMetrics_StopIDsDeduplicatedAcrossTrips guards the fix for the same
// comparison: Java's MonitoredResult tracks matched/unmatched stop IDs as
// Sets, so a stop served by many trips in one poll counts once, not once per
// trip that passes through it.
func TestGetMetrics_StopIDsDeduplicatedAcrossTrips(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1")
	mustCreateTrip(t, manager, "T2", "R1")
	mustCreateStop(t, manager, "SHARED_STOP")

	knownStop := "SHARED_STOP"
	unknownStop := "GHOST_STOP"
	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{
			ID:              gtfs.TripID{ID: "T1", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &knownStop}, {StopID: &unknownStop}},
		},
		{
			ID:              gtfs.TripID{ID: "T2", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &knownStop}, {StopID: &unknownStop}},
		},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.StopIDsMatchedCount["A"])
	assert.Equal(t, 1, snapshot.StopIDsUnmatchedCount["A"])
	assert.Equal(t, []string{"GHOST_STOP"}, snapshot.StopIDsUnmatched["A"])
}

func TestGetMetrics_UnmatchedStops(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1")
	mustCreateStop(t, manager, "S1")

	knownStop := "S1"
	unknownStop := "GHOST_STOP"
	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{
			ID: gtfs.TripID{ID: "T1", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{
				{StopID: &knownStop},
				{StopID: &unknownStop},
			},
		},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.StopIDsMatchedCount["A"])
	assert.Equal(t, 1, snapshot.StopIDsUnmatchedCount["A"])
	assert.Equal(t, []string{"GHOST_STOP"}, snapshot.StopIDsUnmatched["A"])
}

func TestGetMetrics_FeedAgencyFilterFallback(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1")

	// The trip ID is unmatched and its route ID doesn't resolve statically
	// either, so agency attribution can only happen via the feed's
	// configured `agency-ids` filter.
	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "GHOST", RouteID: ""}},
	}
	manager.feedAgencyFilter["feed-1"] = map[string]bool{"A": true}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.RealtimeRecordsTotal["A"])
	assert.Equal(t, 1, snapshot.RealtimeTripCountsUnmatched["A"])
	assert.Equal(t, []string{"GHOST"}, snapshot.RealtimeTripIDsUnmatched["A"])
}

func TestGetMetrics_UnresolvableFeedIsNotAttributed(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "T1", "R1")

	// No feed agency filter and no resolvable static route: this feed's
	// records can't be attributed to any agency.
	manager.feedTrips["feed-1"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "GHOST", RouteID: ""}},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 0, snapshot.RealtimeRecordsTotal["A"])
	assert.Equal(t, 0, snapshot.RealtimeTripCountsUnmatched["A"])
}

func TestGetMetrics_TimeSinceLastRealtimeUpdate(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	manager.feedTrips["feed-1"] = []gtfs.Trip{}
	manager.feedAgencyFilter["feed-1"] = map[string]bool{"A": true}
	// Staleness is always measured against the real wall clock (feedLastUpdate
	// is stamped with time.Now() elsewhere), independent of metricsTestNow.
	manager.SetFeedUpdateTimeForTest("feed-1", time.Now().Add(-30*time.Second))

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.InDelta(t, 30, snapshot.TimeSinceLastRealtimeUpdate["A"], 5)
}

func TestGetMetrics_MultipleFeedsIsolateAgencies(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"RA": {Id: "RA", Agency: &gtfs.Agency{Id: "A"}},
		"RB": {Id: "RB", Agency: &gtfs.Agency{Id: "B"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "TA1", "RA")
	mustCreateTrip(t, manager, "TB1", "RB")

	manager.feedTrips["feed-a"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "TA1", RouteID: "RA"}, StopTimeUpdates: activeStopTimeUpdates()},
	}
	manager.feedTrips["feed-b"] = []gtfs.Trip{
		{ID: gtfs.TripID{ID: "TB1", RouteID: "RB"}, StopTimeUpdates: activeStopTimeUpdates()},
		{ID: gtfs.TripID{ID: "GHOST_B", RouteID: "RB"}},
	}

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.RealtimeRecordsTotal["A"])
	assert.Equal(t, 1, snapshot.RealtimeTripCountsMatched["A"])
	assert.Equal(t, 0, snapshot.RealtimeTripCountsUnmatched["A"])

	assert.Equal(t, 2, snapshot.RealtimeRecordsTotal["B"])
	assert.Equal(t, 1, snapshot.RealtimeTripCountsMatched["B"])
	assert.Equal(t, 1, snapshot.RealtimeTripCountsUnmatched["B"])
	assert.Equal(t, []string{"GHOST_B"}, snapshot.RealtimeTripIDsUnmatched["B"])
}

// TestGetMetrics_ScheduledTripsCountOnlyCountsActiveTrips guards the fix for
// a discrepancy found by comparing Maglev's output against the production
// Java metrics.json: scheduledTripsCount reports trips active right now
// (matching Java's TripsForAgencyQueryBean, which defaults to the current
// time), not a flat total of every trip ever in the static schedule.
func TestGetMetrics_ScheduledTripsCountOnlyCountsActiveTrips(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)
	mustCreateTrip(t, manager, "ACTIVE1", "R1")
	mustCreateInactiveTrip(t, manager, "INACTIVE1", "R1")

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.ScheduledTripsCount["A"],
		"only the trip whose window covers metricsTestNow should count")
}

// TestGetMetrics_ScheduledTripsCountHasNoRunningLateEarlyTolerance guards
// against reintroducing trips-for-route's 30-minute-late/10-minute-early
// buffer here: upstream's agency-level query
// (BlockStatusServiceImpl#getActiveBlocksForAgency) queries with
// timeFrom == timeTo == now, so a trip that ended shortly before, or starts
// shortly after, metricsTestNow must not count.
func TestGetMetrics_ScheduledTripsCountHasNoRunningLateEarlyTolerance(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	const serviceID = "service-1"
	mustCreateCalendar(t, manager, serviceID)

	// Ended 15 minutes before metricsTestNow: within the old 30-minute
	// running-late buffer, but not currently active.
	_, err := manager.GtfsDB.Queries.CreateTrip(context.Background(), gtfsdb.CreateTripParams{
		ID:               "RECENTLY_ENDED",
		RouteID:          "R1",
		ServiceID:        serviceID,
		MinArrivalTime:   sql.NullInt64{Int64: (metricsTestNowSinceMidnight - time.Hour).Nanoseconds(), Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (metricsTestNowSinceMidnight - 15*time.Minute).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)

	// Starts 5 minutes after metricsTestNow: within the old 10-minute
	// running-early buffer, but not yet active.
	_, err = manager.GtfsDB.Queries.CreateTrip(context.Background(), gtfsdb.CreateTripParams{
		ID:               "STARTS_SOON",
		RouteID:          "R1",
		ServiceID:        serviceID,
		MinArrivalTime:   sql.NullInt64{Int64: (metricsTestNowSinceMidnight + 5*time.Minute).Nanoseconds(), Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (metricsTestNowSinceMidnight + time.Hour).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 0, snapshot.ScheduledTripsCount["A"],
		"trips just outside metricsTestNow must not count, even within the trips-for-route buffer")
}

// TestGetMetrics_ScheduledTripsCountIncludesLayoverBlocks guards the other
// half of the getActiveBlocksForAgency fix: a block laying over between two
// of its trips still counts as active, even though no single trip's own
// schedule window covers metricsTestNow.
func TestGetMetrics_ScheduledTripsCountIncludesLayoverBlocks(t *testing.T) {
	routes := map[string]*gtfs.Route{
		"R1": {Id: "R1", Agency: &gtfs.Agency{Id: "A"}},
	}
	manager := newTestManagerWithRoutes(routes)

	const serviceID = "service-1"
	mustCreateCalendar(t, manager, serviceID)
	mustCreateStop(t, manager, "LAYOVER_STOP")

	// The block's next trip departs an hour after metricsTestNow, so no trip
	// is in progress at metricsTestNow, but the vehicle is laying over.
	_, err := manager.GtfsDB.Queries.CreateTrip(context.Background(), gtfsdb.CreateTripParams{
		ID:               "NEXT_LEG",
		RouteID:          "R1",
		ServiceID:        serviceID,
		BlockID:          sql.NullString{String: "BLOCK1", Valid: true},
		MinArrivalTime:   sql.NullInt64{Int64: (metricsTestNowSinceMidnight + time.Hour).Nanoseconds(), Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: (metricsTestNowSinceMidnight + 2*time.Hour).Nanoseconds(), Valid: true},
	})
	require.NoError(t, err)

	err = manager.GtfsDB.Queries.CreateBlockLayover(context.Background(), gtfsdb.CreateBlockLayoverParams{
		BlockID:       "BLOCK1",
		ServiceID:     serviceID,
		RouteID:       "R1",
		LayoverStopID: "LAYOVER_STOP",
		LayoverStart:  (metricsTestNowSinceMidnight - 10*time.Minute).Nanoseconds(),
		LayoverEnd:    (metricsTestNowSinceMidnight + time.Hour).Nanoseconds(),
		NextTripID:    "NEXT_LEG",
	})
	require.NoError(t, err)

	snapshot, err := manager.GetMetrics(context.Background(), metricsTestNow)
	require.NoError(t, err)

	assert.Equal(t, 1, snapshot.ScheduledTripsCount["A"],
		"a block laying over between trips at metricsTestNow should count as active")
}
