package gtfsdb

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

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

	err = client.processAndStoreGTFSDataWithSource(validBytes, "test-source-valid")
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
		assert.NoError(t, err)

		_, err = f.Write([]byte(content))
		assert.NoError(t, err)
	}

	err = w.Close()
	assert.NoError(t, err)

	invalidBytes := buf.Bytes()

	// 3. Attempt to import structurally invalid data
	err = client.processAndStoreGTFSDataWithSource(invalidBytes, "test-source-invalid")
	assert.Error(t, err, "Import should fail structural validation")
	assert.Contains(t, err.Error(), "validation failed")

	// 4. Verify original data was NOT cleared
	countsAfter, err := client.TableCounts()
	assert.NoError(t, err)
	assert.Equal(t, originalRouteCount, countsAfter["routes"], "Database should remain intact after validation failure")
}

// TestSlowQueryDB_LogsSlowQueries verifies that slowQueryDB emits a log record
// when a query exceeds the threshold, and is silent when it does not.
func TestSlowQueryDB_LogsSlowQueries(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS t (v INTEGER)")
	require.NoError(t, err)

	// Capture slog output via a custom handler.
	type logRecord struct {
		msg   string
		level slog.Level
		attrs map[string]any
	}
	var captured []logRecord
	handler := &captureHandler{fn: func(r slog.Record) {
		attrs := make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		captured = append(captured, logRecord{msg: r.Message, level: r.Level, attrs: attrs})
	}}
	logger := slog.New(handler)

	ctx := context.Background()

	// Threshold of 0 → logging disabled; no records should be emitted.
	wrapper := newSlowQueryDB(db, 0)
	wrapper.logger = logger
	rows, err := wrapper.QueryContext(ctx, "SELECT 1")
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	assert.Empty(t, captured, "threshold=0 must not emit any log records")

	// Use a fake clock advancing 10 ms per call to ensure the query exceeds
	// the threshold and avoid Windows timer resolution issues.
	t0 := time.Unix(0, 0)
	call := 0
	wrapper.now = func() time.Time {
		call++
		return t0.Add(time.Duration(call) * 10 * time.Millisecond)
	}
	wrapper.threshold = 1 * time.Nanosecond
	rows, err = wrapper.QueryContext(ctx, "SELECT 1")
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NotEmpty(t, captured, "threshold=1ns must emit a slow_query record")
	assert.Equal(t, "slow_query", captured[0].msg)
	assert.Equal(t, slog.LevelWarn, captured[0].level)
	assert.Equal(t, "QueryContext", captured[0].attrs["op"])
}

func TestSlowQueryDB_LogsSlowExecContext(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS t (v INTEGER)")
	require.NoError(t, err)

	type logRecord struct {
		msg   string
		level slog.Level
		attrs map[string]any
	}
	var captured []logRecord
	handler := &captureHandler{fn: func(r slog.Record) {
		attrs := make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		captured = append(captured, logRecord{msg: r.Message, level: r.Level, attrs: attrs})
	}}
	logger := slog.New(handler)

	wrapper := newSlowQueryDB(db, 1*time.Nanosecond)
	wrapper.logger = logger
	t0 := time.Unix(0, 0)
	call := 0
	wrapper.now = func() time.Time {
		call++
		return t0.Add(time.Duration(call) * 10 * time.Millisecond)
	}

	_, err = wrapper.ExecContext(context.Background(), "INSERT INTO t(v) VALUES (1)")
	require.NoError(t, err)
	require.Len(t, captured, 1)
	assert.Equal(t, "slow_query", captured[0].msg)
	assert.Equal(t, "ExecContext", captured[0].attrs["op"])
}

func TestSlowQueryDB_LogsSlowQueryRowContext(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	type logRecord struct {
		msg   string
		level slog.Level
		attrs map[string]any
	}
	var captured []logRecord
	handler := &captureHandler{fn: func(r slog.Record) {
		attrs := make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		captured = append(captured, logRecord{msg: r.Message, level: r.Level, attrs: attrs})
	}}
	logger := slog.New(handler)

	wrapper := newSlowQueryDB(db, 1*time.Nanosecond)
	wrapper.logger = logger
	t0 := time.Unix(0, 0)
	call := 0
	wrapper.now = func() time.Time {
		call++
		return t0.Add(time.Duration(call) * 10 * time.Millisecond)
	}

	var v int
	err = wrapper.QueryRowContext(context.Background(), "SELECT 1").Scan(&v)
	require.NoError(t, err)
	assert.Equal(t, 1, v)
	require.Len(t, captured, 1)
	assert.Equal(t, "slow_query", captured[0].msg)
	assert.Equal(t, "QueryRowContext", captured[0].attrs["op"])
}

func TestSlowQueryDB_LogsSlowErrorsWithErrorAttribute(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	type logRecord struct {
		msg   string
		level slog.Level
		attrs map[string]any
	}
	var captured []logRecord
	handler := &captureHandler{fn: func(r slog.Record) {
		attrs := make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		captured = append(captured, logRecord{msg: r.Message, level: r.Level, attrs: attrs})
	}}
	logger := slog.New(handler)

	wrapper := newSlowQueryDB(db, 1*time.Nanosecond)
	wrapper.logger = logger
	t0 := time.Unix(0, 0)
	call := 0
	wrapper.now = func() time.Time {
		call++
		return t0.Add(time.Duration(call) * 10 * time.Millisecond)
	}

	_, err = wrapper.QueryContext(context.Background(), "SELECT * FROM missing_table")
	require.Error(t, err)
	require.Len(t, captured, 1)
	assert.Equal(t, "slow_query", captured[0].msg)
	assert.Equal(t, "QueryContext", captured[0].attrs["op"])
	errAttr, ok := captured[0].attrs["error"].(string)
	require.True(t, ok)
	assert.Contains(t, errAttr, "no such table")
}

func TestParseSlowQueryThreshold(t *testing.T) {
	t.Run("empty value disables logging", func(t *testing.T) {
		warned := false
		got := parseSlowQueryThreshold("", func(string, ...any) { warned = true })
		assert.Equal(t, time.Duration(0), got)
		assert.False(t, warned)
	})

	t.Run("valid positive integer enables logging", func(t *testing.T) {
		warned := false
		got := parseSlowQueryThreshold("25", func(string, ...any) { warned = true })
		assert.Equal(t, 25*time.Millisecond, got)
		assert.False(t, warned)
	})

	t.Run("invalid value logs warning and disables logging", func(t *testing.T) {
		warned := false
		got := parseSlowQueryThreshold("50ms", func(string, ...any) { warned = true })
		assert.Equal(t, time.Duration(0), got)
		assert.True(t, warned)
	})

	t.Run("non-positive value logs warning and disables logging", func(t *testing.T) {
		warned := false
		got := parseSlowQueryThreshold("-5", func(string, ...any) { warned = true })
		assert.Equal(t, time.Duration(0), got)
		assert.True(t, warned)
	})
}

// TestTrimQuery verifies whitespace collapse and truncation.
func TestTrimQuery(t *testing.T) {
	long := "SELECT " + string(make([]byte, 200))
	result := trimQuery(long)
	assert.LessOrEqual(t, len(result), 124, "trimQuery must truncate to ≤120 chars + ellipsis")
	assert.True(t, len(trimQuery("  SELECT\n  1  ")) < len("  SELECT\n  1  "),
		"trimQuery must collapse whitespace")
}

// captureHandler is a minimal slog.Handler that calls fn for every record.
type captureHandler struct {
	fn func(slog.Record)
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool  { return true }
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler          { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler               { return h }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error { h.fn(r); return nil }
