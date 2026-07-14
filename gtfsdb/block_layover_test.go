package gtfsdb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// newTestClientWithRABA builds a fresh in-memory client populated from testdata/raba.zip.
func newTestClientWithRABA(t *testing.T) *Client {
	t.Helper()

	client, err := NewClient(Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	rabaBytes, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)

	parsed, err := ParseGtfsData(rabaBytes, "test-raba")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)

	return client
}

func TestBuildBlockLayoverIndex_PopulatesTable(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var total int
	err := client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM block_layover").Scan(&total)
	require.NoError(t, err)
	assert.Greater(t, total, 0, "RABA feed should produce at least one block layover")

	// NOT NULL and the layover_start <= layover_end invariant are enforced by the
	// table schema. Here we only verify the builder doesn't emit empty-string keys
	// (NOT NULL still allows "").
	rows, err := client.DB.QueryContext(ctx, `
		SELECT block_id, route_id, service_id, layover_stop_id, next_trip_id
		FROM block_layover`)
	require.NoError(t, err)
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var blockID, routeID, serviceID, stopID, nextTripID string
		require.NoError(t, rows.Scan(&blockID, &routeID, &serviceID, &stopID, &nextTripID))

		assert.NotEmpty(t, blockID)
		assert.NotEmpty(t, routeID)
		assert.NotEmpty(t, serviceID)
		assert.NotEmpty(t, stopID)
		assert.NotEmpty(t, nextTripID)
		checked++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, total, checked)
}

func TestBuildBlockLayoverIndex_RebuildOnReimport(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var first int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM block_layover").Scan(&first))
	require.Greater(t, first, 0)

	// Reimport the same feed; the builder clears the table first, so the row
	// count must stay identical (not double).
	rabaBytes, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)
	parsed, err := ParseGtfsData(rabaBytes, "test-raba-reimport")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	var second int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM block_layover").Scan(&second))
	assert.Equal(t, first, second, "reimport must not accumulate duplicate layover rows")
}

// TestBuildBlockLayoverIndex_BoundsMatchJava verifies the recorded window is
// [prev-trip last-stop DEPARTURE, next-trip first-stop ARRIVAL] as defined by
// Java's BlockTripLayoverTimeComparator. Using arrival/departure instead would
// widen the window into in-service dwell time at both ends and cause
// trips-for-route to falsely report the block as in layover while the vehicle
// is still running its scheduled trip.
// TestBuildBlockLayoverIndex_BoundsMatchJava drives the builder with two
// hand-crafted trips in the same block that share a terminal stop with real
// scheduled dwell time. It pins the recorded window to Java's
// BlockTripLayoverTimeComparator definition: layover_start = prev trip's
// last-stop DEPARTURE, layover_end = next trip's first-stop ARRIVAL. Under
// the previous (wrong) code, layover_start used the last-stop ARRIVAL and
// layover_end used the first-stop DEPARTURE, widening the window by the dwell
// at both ends.
func TestBuildBlockLayoverIndex_BoundsMatchJava(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	route := &gtfs.Route{Id: "R1"}
	svc := &gtfs.Service{Id: "SVC-WEEKDAY"}
	terminalStop := &gtfs.Stop{Id: "TERMINAL"}
	otherStop := &gtfs.Stop{Id: "OTHER"}

	// Trip A: OTHER 09:00 → TERMINAL, arrives 10:00, departs 10:20 (20-min dwell).
	// Trip B: TERMINAL, arrives 10:35, departs 10:40 (5-min dwell) → OTHER 11:00.
	// Java's window: [10:20, 10:35] (15 min). Old-buggy window: [10:00, 10:40] (40 min).
	trips := []gtfs.ScheduledTrip{
		{
			ID: "trip-A", Route: route, Service: svc, BlockID: "BLOCK-1",
			StopTimes: []gtfs.ScheduledStopTime{
				{Stop: otherStop, StopSequence: 1, ArrivalTime: 9 * time.Hour, DepartureTime: 9 * time.Hour},
				{Stop: terminalStop, StopSequence: 2,
					ArrivalTime:   10*time.Hour + 0*time.Minute,
					DepartureTime: 10*time.Hour + 20*time.Minute},
			},
		},
		{
			ID: "trip-B", Route: route, Service: svc, BlockID: "BLOCK-1",
			StopTimes: []gtfs.ScheduledStopTime{
				{Stop: terminalStop, StopSequence: 1,
					ArrivalTime:   10*time.Hour + 35*time.Minute,
					DepartureTime: 10*time.Hour + 40*time.Minute},
				{Stop: otherStop, StopSequence: 2, ArrivalTime: 11 * time.Hour, DepartureTime: 11 * time.Hour},
			},
		},
	}
	staticData := &gtfs.Static{Trips: trips}

	// Seed the trips table so the block_layover FK on next_trip_id resolves.
	// Disable FK enforcement during setup so we can avoid seeding the full
	// routes/calendar chain — this test only exercises layover bounds math.
	_, err = client.DB.Exec(`PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	_, err = client.DB.Exec(`INSERT INTO trips (id, route_id, service_id, block_id) VALUES
		('trip-A', 'R1', 'SVC-WEEKDAY', 'BLOCK-1'),
		('trip-B', 'R1', 'SVC-WEEKDAY', 'BLOCK-1')`)
	require.NoError(t, err)
	_, err = client.DB.Exec(`PRAGMA foreign_keys = ON`)
	require.NoError(t, err)

	require.NoError(t, client.buildBlockLayoverIndex(context.Background(), staticData, nil))

	var start, end int64
	var recordedStopID, recordedNextTripID string
	err = client.DB.QueryRow(`
		SELECT layover_stop_id, next_trip_id, layover_start, layover_end
		FROM block_layover WHERE block_id = 'BLOCK-1'`,
	).Scan(&recordedStopID, &recordedNextTripID, &start, &end)
	require.NoError(t, err)

	assert.Equal(t, "TERMINAL", recordedStopID)
	assert.Equal(t, "trip-B", recordedNextTripID)

	expectedStart := int64((10*time.Hour + 20*time.Minute))
	expectedEnd := int64((10*time.Hour + 35*time.Minute))
	assert.Equal(t, expectedStart, start,
		"layover_start must be prev trip's last-stop DEPARTURE (10:20), not arrival (10:00)")
	assert.Equal(t, expectedEnd, end,
		"layover_end must be next trip's first-stop ARRIVAL (10:35), not departure (10:40)")
}

func TestGetActiveLayoverBlockIDsForRoute_MatchesWindow(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// Pull a real layover from the built index to drive the query with known-good inputs.
	var routeID, serviceID, blockID string
	var start, end int64
	err := client.DB.QueryRowContext(ctx,
		`SELECT route_id, service_id, block_id, layover_start, layover_end FROM block_layover LIMIT 1`,
	).Scan(&routeID, &serviceID, &blockID, &start, &end)
	require.NoError(t, err)

	// Window that straddles the layover: [start-1s, end+1s] must match.
	hits, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     []string{serviceID},
		TimeRangeStart: start - 1_000_000_000,
		TimeRangeEnd:   end + 1_000_000_000,
	})
	require.NoError(t, err)
	assert.Contains(t, hits, blockID, "block should be returned when window overlaps its layover")

	// Window entirely after the layover: [end+1s, end+2s] must NOT match.
	miss, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     []string{serviceID},
		TimeRangeStart: end + 1_000_000_000,
		TimeRangeEnd:   end + 2_000_000_000,
	})
	require.NoError(t, err)
	assert.NotContains(t, miss, blockID, "block must not be returned when window is after its layover")

	// Wrong route: must return empty.
	wrongRoute, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        "__not_a_real_route__",
		ServiceIds:     []string{serviceID},
		TimeRangeStart: start - 1_000_000_000,
		TimeRangeEnd:   end + 1_000_000_000,
	})
	require.NoError(t, err)
	assert.Empty(t, wrongRoute)

	// Wrong service: must return empty.
	wrongService, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     []string{"__not_a_real_service__"},
		TimeRangeStart: start - 1_000_000_000,
		TimeRangeEnd:   end + 1_000_000_000,
	})
	require.NoError(t, err)
	assert.Empty(t, wrongService)
}
