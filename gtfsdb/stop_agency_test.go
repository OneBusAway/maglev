package gtfsdb

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// Java OneBusAway names every stop after its feed's default agency, the first
// row of agency.txt, whichever agencies' routes serve it.
func TestBuildStopAgencyIndex_NamesEveryStopAfterTheFirstAgency(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	addSecondAgencyAtAStop(t, client)
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO stops (id, name, lat, lon) VALUES ('unserved', 'Unserved', 40.5, -122.3)
	`)
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))

	assert.Equal(t, []string{"25"}, distinctStopAgencies(t, client))
	assert.Zero(t, stopsNotIndexedOnce(t, client), "every stop, served or not, should have exactly one row")
}

func TestBuildStopAgencyIndex_RebuildsFromScratch(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	stopID := addSecondAgencyAtAStop(t, client)

	// A stale row from a previous feed version must not survive a rebuild.
	_, err := client.DB.ExecContext(ctx,
		"INSERT INTO stop_agencies (stop_id, agency_id) VALUES (?, ?)", stopID, secondAgencyID)
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))

	assert.Equal(t, []string{"25"}, distinctStopAgencies(t, client))
}

func TestBackfillStopAgencyIndex_FillsEmptyIndex(t *testing.T) {
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

	// Stand in for a database imported before stop_agencies existed: the feed is loaded
	// but the index is empty, and the unchanged feed hash means no import will rebuild it.
	_, err = client.DB.ExecContext(ctx, "DELETE FROM stop_agencies")
	require.NoError(t, err)
	require.NoError(t, client.Close())

	// Reopen through NewClient, the actual startup path, rather than calling
	// backfillStopAgencyIndex directly.
	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	var indexed int
	err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_agencies").Scan(&indexed)
	require.NoError(t, err)
	assert.Greater(t, indexed, 0, "NewClient should backfill an empty index on open")
}

func TestBackfillStopAgencyIndex_RebuildsLegacySingleColumnPK(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// Stand in for a database created under the pre-composite-key schema: the table
	// exists with a single-column stop_id PK, already populated with one row per stop.
	_, err := client.DB.ExecContext(ctx, `
		DROP TABLE stop_agencies;
		CREATE TABLE stop_agencies (
			stop_id TEXT PRIMARY KEY,
			agency_id TEXT NOT NULL
		);
	`)
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stop_agencies (stop_id, agency_id)
		SELECT DISTINCT stop_id, (SELECT id FROM agencies LIMIT 1) FROM stop_times
	`)
	require.NoError(t, err)

	require.NoError(t, client.backfillStopAgencyIndex(ctx))

	var pkColumns int
	err = client.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('stop_agencies') WHERE pk > 0`,
	).Scan(&pkColumns)
	require.NoError(t, err)
	assert.Equal(t, 2, pkColumns, "rebuild should leave the table with the composite primary key")
}

func TestBackfillStopAgencyIndex_RollsBackOnFailure(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// Stand in for a database created under the pre-composite-key schema, so the backfill
	// takes the transactional recreate-and-rebuild path rather than skipping outright.
	_, err := client.DB.ExecContext(ctx, `
		DROP TABLE stop_agencies;
		CREATE TABLE stop_agencies (
			stop_id TEXT PRIMARY KEY,
			agency_id TEXT NOT NULL
		);
	`)
	require.NoError(t, err)
	legacy, err := client.DB.ExecContext(ctx, `
		INSERT INTO stop_agencies (stop_id, agency_id)
		SELECT DISTINCT stop_id, (SELECT id FROM agencies LIMIT 1) FROM stop_times
	`)
	require.NoError(t, err)
	legacyRows, err := legacy.RowsAffected()
	require.NoError(t, err)

	// Take the name of the index the rebuild creates, so it fails after dropping
	// and recreating the table.
	_, err = client.DB.ExecContext(ctx, "CREATE INDEX idx_stop_agencies_agency ON agencies (name)")
	require.NoError(t, err)

	err = client.backfillStopAgencyIndex(ctx)
	require.Error(t, err, "a failure mid-rebuild should surface, not be swallowed")

	var pkColumns int
	err = client.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('stop_agencies') WHERE pk > 0`,
	).Scan(&pkColumns)
	require.NoError(t, err)
	assert.Equal(t, 1, pkColumns,
		"a rolled-back transaction should leave the legacy table exactly as the DROP found it, not half-recreated")

	var count int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_agencies").Scan(&count))
	assert.Equal(t, legacyRows, int64(count),
		"a rolled-back transaction should leave the legacy table's row count untouched")
}

func TestBackfillStopAgencyIndex_SkipsEmptyFeed(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.backfillStopAgencyIndex(context.Background()))
}

// A database imported when stop_agencies held one row per agency serving a stop
// keeps that index until its feed changes, unless startup rebuilds it.
func TestBackfillStopAgencyIndex_RebuildsRouteBasedIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	stopID := addSecondAgencyAtAStop(t, client)
	_, err := client.DB.ExecContext(ctx,
		"INSERT INTO stop_agencies (stop_id, agency_id) VALUES (?, ?)", stopID, secondAgencyID)
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "DELETE FROM stop_agencies WHERE stop_id = (SELECT MAX(id) FROM stops)")
	require.NoError(t, err)

	require.NoError(t, client.backfillStopAgencyIndex(ctx))

	assert.Equal(t, []string{"25"}, distinctStopAgencies(t, client))
	assert.Zero(t, stopsNotIndexedOnce(t, client))
}

func TestBackfillStopAgencyIndex_SettlesAfterRebuild(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_agencies")
	require.NoError(t, err)
	require.NoError(t, client.backfillStopAgencyIndex(ctx))

	// backfillStopAgencyIndex logs "rebuilding stop agency index" only when it
	// runs the rebuild, so a settled index must log nothing.
	previousLogger := slog.Default()
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	err = client.backfillStopAgencyIndex(ctx)
	slog.SetDefault(previousLogger)
	require.NoError(t, err)
	assert.NotContains(t, logBuf.String(), "rebuilding stop agency index")
}

const secondAgencyID = "second-agency"

// addSecondAgencyAtAStop gives one RABA stop a stop time on a route of an
// agency listed after RABA's, and returns the stop's ID.
func addSecondAgencyAtAStop(t *testing.T, client *Client) string {
	t.Helper()
	ctx := context.Background()
	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_times LIMIT 1").Scan(&stopID))
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone) VALUES (?, 'Second Agency', 'http://example.com', 'UTC');
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('second-route', ?, 'S', 3);
		INSERT INTO trips (id, route_id, service_id) VALUES ('second-trip', 'second-route', (SELECT service_id FROM trips LIMIT 1));
	`, secondAgencyID, secondAgencyID)
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, arrival_time, departure_time, stop_sequence)
		VALUES ('second-trip', ?, 0, 0, 0)
	`, stopID)
	require.NoError(t, err)
	return stopID
}

func distinctStopAgencies(t *testing.T, client *Client) []string {
	t.Helper()
	rows, err := client.DB.Query("SELECT DISTINCT agency_id FROM stop_agencies ORDER BY agency_id")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var agencyIDs []string
	for rows.Next() {
		var agencyID string
		require.NoError(t, rows.Scan(&agencyID))
		agencyIDs = append(agencyIDs, agencyID)
	}
	require.NoError(t, rows.Err())
	return agencyIDs
}

func stopsNotIndexedOnce(t *testing.T, client *Client) int {
	t.Helper()
	var count int
	require.NoError(t, client.DB.QueryRow(`
		SELECT COUNT(*) FROM stops
		WHERE (SELECT COUNT(*) FROM stop_agencies WHERE stop_id = stops.id) != 1
	`).Scan(&count))
	return count
}
