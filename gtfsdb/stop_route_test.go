package gtfsdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// stopRoutePairsFromStopTimes is the (stop, route) pair set stop_routes must equal.
const stopRoutePairsFromStopTimes = `
	SELECT DISTINCT stop_times.stop_id, routes.id
	FROM stop_times
	JOIN trips ON stop_times.trip_id = trips.id
	JOIN routes ON trips.route_id = routes.id
`

// countRows returns the single integer the query selects.
func countRows(t *testing.T, client *Client, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, client.DB.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

func TestBuildStopRouteIndex_MatchesStopTimesJoin(t *testing.T) {
	client := newTestClientWithRABA(t)

	assert.Positive(t, countRows(t, client, "SELECT COUNT(*) FROM stop_routes"))
	assert.Zero(t, countRows(t, client, `SELECT COUNT(*) FROM (`+stopRoutePairsFromStopTimes+`
		EXCEPT SELECT stop_id, route_id FROM stop_routes)`), "every pair from the join should be indexed")
	assert.Zero(t, countRows(t, client, `SELECT COUNT(*) FROM (SELECT stop_id, route_id FROM stop_routes
		EXCEPT `+stopRoutePairsFromStopTimes+`)`), "no indexed pair should be missing from the join")
}

func TestBuildStopRouteIndex_KeepsEveryRouteServingAStop(t *testing.T) {
	client := newTestClientWithRABA(t)
	stopID := addSecondRouteAtStop(t, client)

	assert.Equal(t, 1, countRows(t, client,
		"SELECT COUNT(*) FROM stop_routes WHERE stop_id = ? AND route_id = 'second-route'", stopID))
	assert.Greater(t, countRows(t, client, "SELECT COUNT(*) FROM stop_routes WHERE stop_id = ?", stopID), 1)
}

func TestBuildStopRouteIndex_RebuildsFromScratch(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// A stale pair from a previous feed version must not survive a rebuild.
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO stop_routes (stop_id, route_id)
		SELECT id, (SELECT id FROM routes LIMIT 1) FROM stops
		WHERE id NOT IN (SELECT stop_id FROM stop_routes) LIMIT 1
	`)
	require.NoError(t, err)

	require.NoError(t, buildStopRouteIndex(ctx, client.Queries))

	assert.Zero(t, countRows(t, client, `SELECT COUNT(*) FROM (SELECT stop_id, route_id FROM stop_routes
		EXCEPT `+stopRoutePairsFromStopTimes+`)`), "rebuild should drop pairs no trip produces")
}

func TestBuildStopRouteIndex_SkipsTripWithMissingRoute(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// FK checks are off just long enough to write a trip whose route doesn't exist.
	_, err := client.DB.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO trips (id, route_id, service_id) VALUES ('orphan-trip', 'no-such-route', (SELECT service_id FROM trips LIMIT 1))
	`)
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, arrival_time, departure_time, stop_sequence)
		SELECT 'orphan-trip', id, 0, 0, 0 FROM stops LIMIT 1
	`)
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	require.NoError(t, buildStopRouteIndex(ctx, client.Queries))

	assert.Zero(t, countRows(t, client, "SELECT COUNT(*) FROM stop_routes WHERE route_id = 'no-such-route'"))
}

func TestClearAllGTFSData_ClearsStopRoutes(t *testing.T) {
	client := newTestClientWithRABA(t)

	require.NoError(t, client.clearAllGTFSDataWithQueries(context.Background(), client.Queries))

	assert.Zero(t, countRows(t, client, "SELECT COUNT(*) FROM stop_routes"))
}

func TestBackfillStopRouteIndex_FillsEmptyIndex(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "raba.db")

	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	rabaBytes, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)
	parsed, err := ParseGtfsData(rabaBytes, "test-raba")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	// Stand in for a database imported before stop_routes existed: the unchanged feed
	// hash means no import will fill it.
	_, err = client.DB.ExecContext(ctx, "DELETE FROM stop_routes")
	require.NoError(t, err)
	require.NoError(t, client.Close())

	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	assert.Positive(t, countRows(t, client, "SELECT COUNT(*) FROM stop_routes"),
		"NewClient should backfill an empty index on open")
}

func TestBackfillStopRouteIndex_LeavesPopulatedIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var stopID, routeID string
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT stop_id, route_id FROM stop_routes LIMIT 1").Scan(&stopID, &routeID))
	_, err := client.DB.ExecContext(ctx,
		"DELETE FROM stop_routes WHERE stop_id = ? AND route_id = ?", stopID, routeID)
	require.NoError(t, err)

	require.NoError(t, client.backfillStopRouteIndex(ctx))

	assert.Zero(t, countRows(t, client,
		"SELECT COUNT(*) FROM stop_routes WHERE stop_id = ? AND route_id = ?", stopID, routeID),
		"a populated index is the import's to maintain, not the backfill's")
}

func TestBackfillStopRouteIndex_SkipsEmptyFeed(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.backfillStopRouteIndex(context.Background()))
	assert.Zero(t, countRows(t, client, "SELECT COUNT(*) FROM stop_routes"))
}

// addSecondRouteAtStop makes one RABA stop also served by a route that sorts before
// RABA's own, rebuilds the index, and returns the stop's ID.
func addSecondRouteAtStop(t *testing.T, client *Client) string {
	t.Helper()
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_routes LIMIT 1").Scan(&stopID))

	for _, stmt := range []string{
		`INSERT INTO routes (id, agency_id, short_name, type) VALUES ('second-route', (SELECT id FROM agencies LIMIT 1), 'S', 3)`,
		`INSERT INTO trips (id, route_id, service_id) VALUES ('second-trip', 'second-route', (SELECT service_id FROM trips LIMIT 1))`,
	} {
		_, err := client.DB.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, arrival_time, departure_time, stop_sequence)
		VALUES ('second-trip', ?, 0, 0, 0)
	`, stopID)
	require.NoError(t, err)

	require.NoError(t, buildStopRouteIndex(ctx, client.Queries))
	return stopID
}
