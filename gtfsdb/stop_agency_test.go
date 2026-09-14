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

func TestBuildStopAgencyIndex_PopulatesTable(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var indexed int
	err := client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_agencies").Scan(&indexed)
	require.NoError(t, err)
	assert.Greater(t, indexed, 0, "RABA feed should produce at least one indexed stop")

	// Every stop a route serves must be indexed, and no other stop may be.
	var missing int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT DISTINCT stop_times.stop_id FROM stop_times
			EXCEPT
			SELECT stop_id FROM stop_agencies
		)
	`).Scan(&missing)
	require.NoError(t, err)
	assert.Zero(t, missing, "every stop with a stop time should have an indexed agency")

	var stray int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stop_agencies
		WHERE stop_id NOT IN (SELECT stop_id FROM stop_times)
	`).Scan(&stray)
	require.NoError(t, err)
	assert.Zero(t, stray, "a stop no route serves should not be indexed")

	// The index must hold exactly the (stop, agency) pairs the join produces - no more, no less.
	sourcePairs := `
		SELECT DISTINCT stop_times.stop_id, routes.agency_id
		FROM stop_times
		JOIN trips ON stop_times.trip_id = trips.id
		JOIN routes ON trips.route_id = routes.id
	`

	var missingPairs int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			`+sourcePairs+`
			EXCEPT
			SELECT stop_id, agency_id FROM stop_agencies
		)
	`).Scan(&missingPairs)
	require.NoError(t, err)
	assert.Zero(t, missingPairs, "every (stop, agency) pair from the join should be indexed")

	var strayPairs int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT stop_id, agency_id FROM stop_agencies
			EXCEPT
			`+sourcePairs+`
		)
	`).Scan(&strayPairs)
	require.NoError(t, err)
	assert.Zero(t, strayPairs, "no indexed pair should be missing from the join")
}

func TestBuildStopAgencyIndex_KeepsEveryAgencyServingAStop(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_agencies LIMIT 1").Scan(&stopID))

	// Give the stop a second agency: a new agency, a route it owns, a trip on that route,
	// and a stop_time at the same stop.
	const secondAgencyID = "second-agency"
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone) VALUES (?, 'Second Agency', 'http://example.com', 'UTC')
	`, secondAgencyID)
	require.NoError(t, err)

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO routes (id, agency_id, short_name, type) VALUES ('second-route', ?, 'S', 3)
	`, secondAgencyID)
	require.NoError(t, err)

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO trips (id, route_id, service_id) VALUES ('second-trip', 'second-route', (SELECT service_id FROM trips LIMIT 1))
	`)
	require.NoError(t, err)

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, arrival_time, departure_time, stop_sequence)
		VALUES ('second-trip', ?, 0, 0, 0)
	`, stopID)
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))

	rows, err := client.DB.QueryContext(ctx, "SELECT agency_id FROM stop_agencies WHERE stop_id = ?", stopID)
	require.NoError(t, err)
	defer rows.Close()

	var agencyIDs []string
	for rows.Next() {
		var agencyID string
		require.NoError(t, rows.Scan(&agencyID))
		agencyIDs = append(agencyIDs, agencyID)
	}
	require.NoError(t, rows.Err())

	assert.GreaterOrEqual(t, len(agencyIDs), 2, "a stop served by two agencies should have two rows, not one collapsed row")
	assert.Contains(t, agencyIDs, secondAgencyID)
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

	var before []struct{ stopID, agencyID string }
	rows, err := client.DB.QueryContext(ctx, "SELECT stop_id, agency_id FROM stop_agencies ORDER BY stop_id, agency_id")
	require.NoError(t, err)
	for rows.Next() {
		var r struct{ stopID, agencyID string }
		require.NoError(t, rows.Scan(&r.stopID, &r.agencyID))
		before = append(before, r)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, before)

	// Stand in for a database created under the pre-composite-key schema, so the backfill
	// takes the transactional recreate-and-rebuild path rather than skipping outright.
	_, err = client.DB.ExecContext(ctx, `
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

	// Point one route at an agency that does not exist, with FK checks off just long
	// enough to create the dangling reference. BuildStopAgencies then tries to insert a
	// stop_agencies row for that agency, which fails its own FK check mid-rebuild.
	_, err = client.DB.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "UPDATE routes SET agency_id = 'no-such-agency' WHERE id = (SELECT id FROM routes LIMIT 1)")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	err = client.backfillStopAgencyIndex(ctx)
	require.Error(t, err, "a foreign key violation mid-rebuild should surface, not be swallowed")

	var pkColumns int
	err = client.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('stop_agencies') WHERE pk > 0`,
	).Scan(&pkColumns)
	require.NoError(t, err)
	assert.Equal(t, 1, pkColumns,
		"a rolled-back transaction should leave the legacy table exactly as the DROP found it, not half-recreated")

	var count int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_agencies").Scan(&count))
	assert.Equal(t, len(before), count,
		"a rolled-back transaction should leave the legacy table's row count untouched")
}

func TestBackfillStopAgencyIndex_SkipsEmptyFeed(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, client.backfillStopAgencyIndex(context.Background()))
}

func TestBuildStopAgencyIndex_RebuildsFromScratch(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// A stale row from a previous feed version must not survive a rebuild.
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO stop_agencies (stop_id, agency_id)
		SELECT id, (SELECT id FROM agencies LIMIT 1) FROM stops
		WHERE id NOT IN (SELECT stop_id FROM stop_agencies) LIMIT 1
	`)
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))

	var stray int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stop_agencies
		WHERE stop_id NOT IN (SELECT stop_id FROM stop_times)
	`).Scan(&stray)
	require.NoError(t, err)
	assert.Zero(t, stray, "rebuild should drop rows for stops no route serves")
}

func TestBackfillStopAgencyIndex_SettlesWhenNoStopTimeJoinsARoute(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_agencies")
	require.NoError(t, err)

	// Replace every stop_times row with one whose trip_id resolves to nothing, so the
	// index has stop_times but none of them join through to a route. FK checks are off
	// just long enough to write the dangling reference.
	_, err = client.DB.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "DELETE FROM stop_times")
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		SELECT 'no-such-trip', 0, 0, id, 0 FROM stops LIMIT 1
	`)
	require.NoError(t, err)
	_, err = client.DB.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	require.NoError(t, client.backfillStopAgencyIndex(ctx),
		"a stop_times row that can't join to a route should settle, not error")

	var indexed int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_agencies").Scan(&indexed))
	assert.Zero(t, indexed, "nothing joins to a route, so the index should stay empty")

	// A second call must settle without rebuilding: capture the default logger, since
	// backfillStopAgencyIndex logs "rebuilding stop agency index" only when it actually
	// runs the rebuild transaction. Before the fix, indexed staying at 0 forever meant
	// every call rebuilt regardless of whether anything was joinable.
	previousLogger := slog.Default()
	var logBuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	err = client.backfillStopAgencyIndex(ctx)
	slog.SetDefault(previousLogger)
	require.NoError(t, err)
	assert.NotContains(t, logBuf.String(), "rebuilding stop agency index",
		"a settled index with nothing joinable should not trigger another rebuild")
}
