package gtfsdb

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/nulls"
)

func TestPerformDatabaseMigration_Idempotency(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	assert.NoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	ctx := context.Background()

	// 1. First run should succeed and create tables
	err = performDatabaseMigration(ctx, db)
	assert.NoError(t, err, "First migration should succeed")

	// 2. Second run should also succeed without error (idempotent IF NOT EXISTS clauses)
	err = performDatabaseMigration(ctx, db)
	assert.NoError(t, err, "Second migration should be idempotent and succeed")
}

func TestPerformDatabaseMigration_ErrorHandling(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	assert.NoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	// This test mutates the package-level ddl variable.
	// Do NOT add t.Parallel() to this test or any test that calls performDatabaseMigration.
	originalDDL := ddl
	defer func() { ddl = originalDDL }()

	// Inject malformed SQL to simulate a corrupted migration file
	ddl = "CREATE TABLE valid_table (id INT); -- migrate\n THIS IS INVALID SQL;"

	ctx := context.Background()
	err = performDatabaseMigration(ctx, db)

	assert.Error(t, err, "Migration should fail on invalid SQL")
	assert.Contains(t, err.Error(), "error executing DDL statement", "Error should wrap the failing context")
}

func TestProcessAndStoreGTFSData_ValidationFailurePreservesData(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	assert.NoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	ctx := context.Background()
	err = performDatabaseMigration(ctx, db)
	assert.NoError(t, err)

	client := &Client{
		DB:      db,
		Queries: New(db),
		config:  Config{Env: appconf.Test},
	}

	// 1. Read and load valid GTFS bytes
	validBytes, err := os.ReadFile("../testdata/gtfs.zip")
	if err != nil {
		t.Skip("Skipping test: testdata/gtfs.zip not found")
	}

	parsedValid, err := ParseGtfsData(validBytes, "test-source-valid")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(t.Context(), parsedValid)
	assert.NoError(t, err, "First import should succeed")

	counts, err := client.TableCounts()
	assert.NoError(t, err)
	assert.Greater(t, counts["routes"], 0, "Valid data should be imported")
	originalRouteCount := counts["routes"]

	// 2. Create a GTFS feed that passes the parser but fails OUR structural validation
	// We do this by creating a trip that has no stop times.
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	files := map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\n1,BrokenFeed,http://test.com,America/Los_Angeles",
		"routes.txt":     "route_id,agency_id,route_short_name,route_type\n1,1,BrokenRoute,3",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\n1,BrokenStop,47.6,-122.3",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n1,1,1,1,1,1,1,1,20230101,20240101",
		"trips.txt":      "route_id,service_id,trip_id\n1,1,trip_1",                     // Trip exists
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n", // But has NO stop times
	}

	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)

		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}

	err = w.Close()
	require.NoError(t, err)

	invalidBytes := buf.Bytes()

	// 3. Attempt to parse+validate structurally invalid data.
	// Validation now happens during ParseGtfsData, before the DB is touched,
	// so the original working data must remain intact.
	_, err = ParseGtfsData(invalidBytes, "test-source-invalid")
	assert.Error(t, err, "Parse should fail structural validation")
	assert.Contains(t, err.Error(), "validation failed")

	// 4. Verify original data was NOT cleared
	countsAfter, err := client.TableCounts()
	assert.NoError(t, err)
	assert.Equal(t, originalRouteCount, countsAfter["routes"], "Database should remain intact after validation failure")
}

type queryMetricCall struct {
	queryName string
	op        string
	hadErr    bool
}

type testQueryMetricsRecorder struct {
	calls []queryMetricCall
}

func (r *testQueryMetricsRecorder) RecordDBQuery(queryName, op string, err error) {
	r.calls = append(r.calls, queryMetricCall{
		queryName: queryName,
		op:        op,
		hadErr:    err != nil,
	})
}

func TestSlowQueryDB_RecordsQueryMetrics(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	recorder := &testQueryMetricsRecorder{}

	wrapper := newMetricsWrapper(db)
	wrapper.queryMetrics = recorder

	_, err = wrapper.QueryContext(ctx, "-- name: ListAgencies :many\nSELECT 1")
	require.NoError(t, err)

	_, err = wrapper.ExecContext(ctx, "-- name: BrokenExec :exec\nTHIS IS INVALID SQL")
	require.Error(t, err)

	row := wrapper.QueryRowContext(ctx, "SELECT 1")
	var n int
	require.NoError(t, row.Scan(&n))
	assert.Equal(t, 1, n)

	require.Len(t, recorder.calls, 3)
	assert.Equal(t, queryMetricCall{queryName: "ListAgencies", op: "query", hadErr: false}, recorder.calls[0])
	assert.Equal(t, queryMetricCall{queryName: "BrokenExec", op: "exec", hadErr: true}, recorder.calls[1])
	assert.Equal(t, queryMetricCall{queryName: "unknown", op: "query_row", hadErr: false}, recorder.calls[2])
}

func TestNewClient_RecordsQueryMetricsWhenOnlyMetricsEnabled(t *testing.T) {
	originalDDL := ddl
	ddl = `
	CREATE TABLE IF NOT EXISTS agencies (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        url TEXT NOT NULL,
        timezone TEXT NOT NULL,
        lang TEXT,
        phone TEXT,
        fare_url TEXT,
        email TEXT
    );
    CREATE TABLE IF NOT EXISTS stop_agencies (
        stop_id TEXT NOT NULL,
        agency_id TEXT NOT NULL,
        PRIMARY KEY (stop_id, agency_id)
    );
    CREATE TABLE IF NOT EXISTS routes (
        id TEXT PRIMARY KEY
    );
    CREATE TABLE IF NOT EXISTS trips (
        id TEXT PRIMARY KEY,
        route_id TEXT NOT NULL,
		block_id TEXT,
		service_id TEXT NOT NULL
    );
	CREATE TABLE IF NOT EXISTS block_trip_entry (
        trip_id TEXT NOT NULL,
        block_id TEXT
    );
    CREATE TABLE IF NOT EXISTS stop_times (
        trip_id TEXT NOT NULL
    );
	CREATE TABLE IF NOT EXISTS stops (
    	id TEXT PRIMARY KEY,
    	code TEXT
	);
`
	t.Cleanup(func() {
		ddl = originalDDL
	})

	recorder := &testQueryMetricsRecorder{}
	config := Config{
		DBPath:               ":memory:",
		Env:                  appconf.Test,
		QueryMetricsRecorder: recorder,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})

	agencies, err := client.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	assert.Empty(t, agencies)

	require.Len(t, recorder.calls, 1)
	assert.Equal(t, queryMetricCall{queryName: "ListAgencies", op: "query", hadErr: false}, recorder.calls[0])
}

func TestExtractQueryName(t *testing.T) {
	testCases := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "sqlc header present",
			query:    "-- name: GetTrip :one\nSELECT * FROM trips",
			expected: "GetTrip",
		},
		{
			name:     "leading blank lines before sqlc header",
			query:    "\n\n-- name: ListStops :many\nSELECT * FROM stops",
			expected: "ListStops",
		},
		{
			name:     "non-sqlc comment only",
			query:    "-- hello\nSELECT 1",
			expected: "unknown",
		},
		{
			name:     "no header",
			query:    "SELECT 1",
			expected: "unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, extractQueryName(tc.query))
		})
	}
}

// TestTrimQuery verifies whitespace collapse and truncation.
func TestTrimQuery(t *testing.T) {
	long := "SELECT " + string(make([]byte, 200))
	result := trimQuery(long)
	assert.LessOrEqual(t, len(result), 124, "trimQuery must truncate to ≤120 chars + ellipsis")
	assert.True(t, len(trimQuery("  SELECT\n  1  ")) < len("  SELECT\n  1  "),
		"trimQuery must collapse whitespace")
}

func TestUpdateFeedExpiresAtFromCalendar(t *testing.T) {
	ctx := context.Background()

	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { assert.NoError(t, db.Close()) }()

	_, err = db.Exec(`
		CREATE TABLE calendar (end_date TEXT);
		CREATE TABLE calendar_dates (date TEXT, exception_type INTEGER);
		CREATE TABLE import_metadata (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			file_hash TEXT NOT NULL,
			import_time INTEGER NOT NULL,
			file_source TEXT NOT NULL,
			feed_expires_at INTEGER
		);
		INSERT INTO import_metadata (id, file_hash, import_time, file_source) VALUES (1, '', 0, '');
	`)
	require.NoError(t, err)

	q := New(db)
	readExpiresAt := func(t *testing.T) sql.NullInt64 {
		t.Helper()
		md, err := q.GetImportMetadata(ctx)
		require.NoError(t, err)
		return md.FeedExpiresAt
	}

	// 1. Empty calendar → feed_expires_at stays NULL.
	require.NoError(t, updateFeedExpiresAtFromCalendar(ctx, q))
	assert.False(t, readExpiresAt(t).Valid, "Should be NULL when no calendar dates exist")

	// 2. Valid calendar end date → parse and persist.
	_, err = db.Exec("INSERT INTO calendar (end_date) VALUES ('20260401')")
	require.NoError(t, err)
	require.NoError(t, updateFeedExpiresAtFromCalendar(ctx, q))
	expected, _ := time.Parse("20060102150405", "20260401235959")
	got := readExpiresAt(t)
	require.True(t, got.Valid)
	assert.Equal(t, expected.Unix(), got.Int64)

	// 3. Re-running with empty calendar resets to NULL.
	_, err = db.Exec("DELETE FROM calendar")
	require.NoError(t, err)
	require.NoError(t, updateFeedExpiresAtFromCalendar(ctx, q))
	assert.False(t, readExpiresAt(t).Valid, "Should reset to NULL after empty re-import")

	// 4. calendar_dates exception_type=1 with later date overrides calendar end_date.
	_, err = db.Exec("INSERT INTO calendar (end_date) VALUES ('20260401')")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO calendar_dates (date, exception_type) VALUES ('20260405', 1)")
	require.NoError(t, err)
	// exception_type=2 (removed) should NOT extend the expiry.
	_, err = db.Exec("INSERT INTO calendar_dates (date, exception_type) VALUES ('20260410', 2)")
	require.NoError(t, err)

	require.NoError(t, updateFeedExpiresAtFromCalendar(ctx, q))
	expected2, _ := time.Parse("20060102150405", "20260405235959")
	got = readExpiresAt(t)
	require.True(t, got.Valid)
	assert.Equal(t, expected2.Unix(), got.Int64)
}

func blocklessTripFeedZip(t *testing.T) []byte {
	t.Helper()

	files := map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\nA,Test,http://example.com,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_short_name,route_type\nR1,A,1,3\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nS1,Stop 1,47.60,-122.30\nS2,Stop 2,47.61,-122.31\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nWK,1,1,1,1,1,1,1,20250101,20351231\n",
		"trips.txt":      "route_id,service_id,trip_id,block_id\nR1,WK,T1,\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,S1,1\nT1,08:10:00,08:10:00,S2,2\n",
	}

	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	return buf.Bytes()
}

func TestStoreGtfsData_BlocklessTripStoresNullBlockID(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	data, err := ParseGtfsData(blocklessTripFeedZip(t), "test-blockless")
	require.NoError(t, err)

	_, err = client.StoreGtfsData(ctx, data)
	require.NoError(t, err)

	var blockIDIsNull bool
	err = client.DB.QueryRowContext(ctx,
		"SELECT block_id IS NULL FROM trips WHERE id = 'T1'").Scan(&blockIDIsNull)
	require.NoError(t, err)
	assert.True(t, blockIDIsNull, "a trip with no block_id should be stored as NULL, not ''")

	tripIDs, err := client.Queries.GetActiveTripsWithNullBlockForRoute(ctx,
		GetActiveTripsWithNullBlockForRouteParams{
			RouteID:        "R1",
			ServiceIds:     []string{"WK"},
			TimeRangeStart: nulls.Int64(0),
			TimeRangeEnd:   nulls.Int64(int64(24 * time.Hour)),
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"T1"}, tripIDs, "the null-block query should find the blockless trip")

	var entryBlockIDIsNull bool
	err = client.DB.QueryRowContext(ctx,
		"SELECT block_id IS NULL FROM block_trip_entry WHERE trip_id = 'T1'").Scan(&entryBlockIDIsNull)
	require.NoError(t, err)
	assert.True(t, entryBlockIDIsNull, "block_trip_entry should also store NULL for a blockless trip")
}
