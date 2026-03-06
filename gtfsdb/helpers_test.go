package gtfsdb

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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
