package gtfsdb

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
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
        route_id TEXT NOT NULL
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

func newFeedExpiryTestDB(t *testing.T) (*sql.DB, *Queries) {
	t.Helper()
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })

	_, err = db.Exec(`
		CREATE TABLE agencies (
			id TEXT PRIMARY KEY, name TEXT, url TEXT, timezone TEXT,
			lang TEXT, phone TEXT, fare_url TEXT, email TEXT
		);
		INSERT INTO agencies (id, name, url, timezone) VALUES ('1', 'Test', 'https://example.com', 'UTC');
		CREATE TABLE calendar (start_date TEXT, end_date TEXT);
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
	return db, New(db)
}

func TestUpdateFeedExpiresAtFromCalendar(t *testing.T) {
	ctx := context.Background()
	db, q := newFeedExpiryTestDB(t)
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
	_, err := db.Exec("INSERT INTO calendar (end_date) VALUES ('20260401')")
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

func TestUpdateFeedExpiresAtFromCalendar_Timezones(t *testing.T) {
	tests := []struct {
		name      string
		date      string
		timezones []string
		want      string
	}{
		{"New York winter", "20270313", []string{"America/New_York"}, "2027-03-14T04:59:59Z"},
		{"Seattle summer", "20270701", []string{"America/Los_Angeles"}, "2027-07-02T06:59:59Z"},
		{"east of UTC", "20270313", []string{"Asia/Tokyo"}, "2027-03-13T14:59:59Z"},
		{"fractional offset", "20270313", []string{"Asia/Kolkata"}, "2027-03-13T18:29:59Z"},
		{"spring forward", "20270314", []string{"America/New_York"}, "2027-03-15T03:59:59Z"},
		{"fall back", "20271107", []string{"America/New_York"}, "2027-11-08T04:59:59Z"},
		{"multiple zones", "20270313", []string{"Asia/Tokyo", "America/Los_Angeles", "America/New_York", "America/Los_Angeles"}, "2027-03-14T07:59:59Z"},
		{"trimmed timezone", "20270313", []string{" America/New_York "}, "2027-03-14T04:59:59Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, q := newFeedExpiryTestDB(t)
			_, err := db.Exec("DELETE FROM agencies")
			require.NoError(t, err)
			for i, tz := range tt.timezones {
				_, err = db.Exec("INSERT INTO agencies (id, name, url, timezone) VALUES (?, 'Test', 'https://example.com', ?)", i, tz)
				require.NoError(t, err)
			}
			_, err = db.Exec("INSERT INTO calendar (end_date) VALUES (?)", tt.date)
			require.NoError(t, err)
			require.NoError(t, updateFeedExpiresAtFromCalendar(t.Context(), q))
			metadata, err := q.GetImportMetadata(t.Context())
			require.NoError(t, err)
			require.True(t, metadata.FeedExpiresAt.Valid)
			assert.Equal(t, tt.want, time.Unix(metadata.FeedExpiresAt.Int64, 0).UTC().Format(time.RFC3339))
		})
	}
}

func TestUpdateFeedExpiresAtFromCalendar_InvalidTimezonePreservesExpiry(t *testing.T) {
	for _, tz := range []string{"", "Invalid/Timezone"} {
		t.Run(tz, func(t *testing.T) {
			db, q := newFeedExpiryTestDB(t)
			_, err := db.Exec("INSERT INTO calendar (end_date) VALUES ('20270313')")
			require.NoError(t, err)
			require.NoError(t, updateFeedExpiresAtFromCalendar(t.Context(), q))
			before, err := q.GetImportMetadata(t.Context())
			require.NoError(t, err)
			_, err = db.Exec("UPDATE agencies SET timezone = ?", tz)
			require.NoError(t, err)
			require.ErrorContains(t, updateFeedExpiresAtFromCalendar(t.Context(), q), "timezone")
			after, err := q.GetImportMetadata(t.Context())
			require.NoError(t, err)
			assert.Equal(t, before.FeedExpiresAt, after.FeedExpiresAt)
		})
	}
}

func TestStoreGtfsData_UnchangedFeedRefreshesExpiry(t *testing.T) {
	db, q := newFeedExpiryTestDB(t)
	_, err := db.Exec(`
		UPDATE agencies SET timezone = 'America/New_York';
		INSERT INTO calendar (end_date) VALUES ('20270313');
		UPDATE import_metadata SET file_hash = 'unchanged-hash', file_source = 'test',
			import_time = 123;
	`)
	require.NoError(t, err)
	oldExpiry := time.Date(2027, time.March, 13, 23, 59, 59, 0, time.UTC)
	require.NoError(t, q.UpdateFeedExpiresAt(t.Context(), sql.NullInt64{Int64: oldExpiry.Unix(), Valid: true}))
	before, err := q.GetImportMetadata(t.Context())
	require.NoError(t, err)
	client := &Client{DB: db, Queries: q}
	changed, err := client.StoreGtfsData(t.Context(), &GtfsData{Hash: before.FileHash, Source: before.FileSource})
	require.NoError(t, err)
	assert.False(t, changed, "Expiry refresh should not trigger a full reimport")
	after, err := q.GetImportMetadata(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "2027-03-14T04:59:59Z", time.Unix(after.FeedExpiresAt.Int64, 0).UTC().Format(time.RFC3339))
	after.FeedExpiresAt = before.FeedExpiresAt
	assert.Equal(t, before, after, "Other import metadata should be preserved")
}

func TestUpdateFeedExpiresAtFromCalendar_MissingAgency(t *testing.T) {
	db, q := newFeedExpiryTestDB(t)
	_, err := db.Exec("DELETE FROM agencies; INSERT INTO calendar (end_date) VALUES ('20270313')")
	require.NoError(t, err)
	require.ErrorContains(t, updateFeedExpiresAtFromCalendar(t.Context(), q), "without an agency timezone")
}

func TestStoreGtfsData_UnchangedFeedExpiryFailure(t *testing.T) {
	db, q := newFeedExpiryTestDB(t)
	_, err := db.Exec(`
		UPDATE import_metadata SET file_hash = 'unchanged-hash', file_source = 'test';
		UPDATE agencies SET timezone = 'Invalid/Timezone';
		INSERT INTO calendar (end_date) VALUES ('20270313');
	`)
	require.NoError(t, err)
	client := &Client{DB: db, Queries: q}
	changed, err := client.StoreGtfsData(t.Context(), &GtfsData{Hash: "unchanged-hash", Source: "test"})
	require.ErrorContains(t, err, "failed to refresh feed expiry")
	assert.False(t, changed)
	metadata, err := q.GetImportMetadata(t.Context())
	require.NoError(t, err)
	assert.False(t, metadata.FeedExpiresAt.Valid)
}

func TestGtfsDirectionIDToDB(t *testing.T) {
	tests := []struct {
		name     string
		input    gtfs.DirectionID
		expected sql.NullInt64
	}{
		{
			name:     "DirectionID_True maps to 1",
			input:    gtfs.DirectionID_True,
			expected: sql.NullInt64{Int64: 1, Valid: true},
		},
		{
			name:     "DirectionID_False maps to 0",
			input:    gtfs.DirectionID_False,
			expected: sql.NullInt64{Int64: 0, Valid: true},
		},
		{
			name:     "DirectionID_Empty/unknown maps to invalid",
			input:    gtfs.DirectionID(0),
			expected: sql.NullInt64{Valid: false},
		},
		{
			name:     "arbitrary invalid enum maps to invalid",
			input:    gtfs.DirectionID(99),
			expected: sql.NullInt64{Valid: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gtfsDirectionIDToDB(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestBoolToInt(t *testing.T) {
	assert.Equal(t, int64(1), boolToInt(true))
	assert.Equal(t, int64(0), boolToInt(false))
}

func TestToNullInt64(t *testing.T) {
	assert.Equal(t, sql.NullInt64{Valid: false}, toNullInt64(0))
	assert.Equal(t, sql.NullInt64{Int64: 42, Valid: true}, toNullInt64(42))
	assert.Equal(t, sql.NullInt64{Int64: -1, Valid: true}, toNullInt64(-1))
}

func TestPickFirstAvailable(t *testing.T) {
	assert.Equal(t, "primary", pickFirstAvailable("primary", "fallback"))
	assert.Equal(t, "fallback", pickFirstAvailable("", "fallback"))
	assert.Equal(t, "", pickFirstAvailable("", ""))
}

func TestLatestAgencyExpiry(t *testing.T) {
	serviceDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)

	t.Run("empty agencies returns error", func(t *testing.T) {
		_, err := latestAgencyExpiry(serviceDate, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot calculate feed expiry without an agency timezone")
	})

	t.Run("empty timezone returns error", func(t *testing.T) {
		agencies := []Agency{
			{ID: "agency_1", Timezone: "   "},
		}
		_, err := latestAgencyExpiry(serviceDate, agencies)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `agency "agency_1" has empty timezone`)
	})

	t.Run("invalid timezone returns error", func(t *testing.T) {
		agencies := []Agency{
			{ID: "agency_1", Timezone: "Invalid/Not_A_Timezone"},
		}
		_, err := latestAgencyExpiry(serviceDate, agencies)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `agency "agency_1" has invalid timezone`)
	})

	t.Run("single agency calculates end of day in its timezone", func(t *testing.T) {
		agencies := []Agency{
			{ID: "agency_1", Timezone: "America/Los_Angeles"},
		}
		expiry, err := latestAgencyExpiry(serviceDate, agencies)
		require.NoError(t, err)

		loc, err := time.LoadLocation("America/Los_Angeles")
		require.NoError(t, err)
		expected := time.Date(2025, 6, 15, 23, 59, 59, 0, loc)
		assert.Equal(t, expected, expiry)
	})

	t.Run("multiple agencies picks latest UTC expiry", func(t *testing.T) {
		// America/Los_Angeles (UTC-7 in summer) ends later in UTC than America/New_York (UTC-4 in summer)
		agencies := []Agency{
			{ID: "ny_agency", Timezone: "America/New_York"},
			{ID: "la_agency", Timezone: "America/Los_Angeles"},
			{ID: "ny_agency_dup", Timezone: "America/New_York"},
		}
		expiry, err := latestAgencyExpiry(serviceDate, agencies)
		require.NoError(t, err)

		locLA, err := time.LoadLocation("America/Los_Angeles")
		require.NoError(t, err)
		expected := time.Date(2025, 6, 15, 23, 59, 59, 0, locLA)
		assert.Equal(t, expected, expiry)
	})
}

func TestFeedDateRange(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	// 1. Empty calendar and calendar_dates returns empty strings.
	start, end, err := client.FeedDateRange(ctx)
	require.NoError(t, err)
	assert.Equal(t, "", start)
	assert.Equal(t, "", end)

	// 2. Calendar entries define initial range.
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('serv1', 1, 1, 1, 1, 1, 0, 0, '20260401', '20260430')
	`)
	require.NoError(t, err)

	start, end, err = client.FeedDateRange(ctx)
	require.NoError(t, err)
	assert.Equal(t, "20260401", start)
	assert.Equal(t, "20260430", end)

	// 3. calendar_dates with exception_type=1 outside calendar range expands both start and end dates.
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO calendar_dates (service_id, date, exception_type)
		VALUES ('serv1', '20260315', 1), ('serv1', '20260515', 1)
	`)
	require.NoError(t, err)

	start, end, err = client.FeedDateRange(ctx)
	require.NoError(t, err)
	assert.Equal(t, "20260315", start)
	assert.Equal(t, "20260515", end)

	// 4. calendar_dates with exception_type=2 (service removed) outside calendar bounds does NOT expand range.
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO calendar_dates (service_id, date, exception_type)
		VALUES ('serv1', '20260101', 2), ('serv1', '20261231', 2)
	`)
	require.NoError(t, err)

	start, end, err = client.FeedDateRange(ctx)
	require.NoError(t, err)
	assert.Equal(t, "20260315", start)
	assert.Equal(t, "20260515", end)
}
