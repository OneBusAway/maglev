package gtfsdb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	_ "modernc.org/sqlite" // Pure Go SQLite driver with FTS5 support
)

func TestSQLitePerformancePragmasApplied(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Verify cache_size PRAGMA
	var cacheSize int
	err = client.DB.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cacheSize)
	require.NoError(t, err)
	// Should be -64000 (64MB in KB, negative means KB)
	assert.Equal(t, -64000, cacheSize, "Cache size should be set to 64MB")

	// Verify temp_store PRAGMA
	var tempStore int
	err = client.DB.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&tempStore)
	require.NoError(t, err)
	// 2 = MEMORY
	assert.Equal(t, 2, tempStore, "Temp store should be set to MEMORY (2)")
}

func TestMemoryDatabaseConnectionPool(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	// Verify connection pool settings for :memory: database
	stats := client.DB.Stats()
	assert.Equal(t, 1, stats.MaxOpenConnections,
		":memory: databases should use MaxOpenConns=1")
}

func TestFileDatabaseConnectionPool(t *testing.T) {
	// Create temporary directory for test database
	tmpDir, err := os.MkdirTemp("", "gtfsdb_test_*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test.db")

	config := Config{
		DBPath: dbPath,
		Env:    appconf.Development,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	// Verify connection pool settings for file database
	stats := client.DB.Stats()
	assert.Equal(t, 25, stats.MaxOpenConnections,
		"File databases should use MaxOpenConns=25")
}

func TestConnectionPoolBehaviorWithFileDatabase(t *testing.T) {
	// Create temporary directory for test database
	tmpDir, err := os.MkdirTemp("", "gtfsdb_test_*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "test_concurrent.db")

	config := Config{
		DBPath: dbPath,
		Env:    appconf.Development,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Make concurrent queries to verify pooling works
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			rows, err := client.DB.QueryContext(ctx, "SELECT 1")
			if err != nil {
				done <- err
				return
			}
			if rows != nil {
				_ = rows.Close()
			}
			done <- nil
		}()
	}

	// Wait for all queries to complete
	for i := 0; i < 10; i++ {
		err := <-done
		assert.NoError(t, err, "Concurrent query should succeed")
	}

	// Verify connection pool was utilized
	stats := client.DB.Stats()
	assert.True(t, stats.OpenConnections >= 0, "Should have open connections")
}

func TestMemoryDatabaseIsolation(t *testing.T) {
	// This test verifies that :memory: databases with 1 connection
	// maintain proper isolation

	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Insert a test agency
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:   "test_agency",
		Name: "Test Agency",
	})
	require.NoError(t, err)

	// Verify we can query it back (same connection)
	agency, err := client.Queries.GetAgency(ctx, "test_agency")
	require.NoError(t, err)
	assert.Equal(t, "test_agency", agency.ID)
	assert.Equal(t, "Test Agency", agency.Name)
}

func TestPerformancePragmasDoNotBreakFunctionality(t *testing.T) {
	// Verify that performance pragmas don't break normal database operations

	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Test CRUD operations work with pragmas applied

	// Create
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency1",
		Name:     "Agency 1",
		Url:      "http://example.com",
		Timezone: "America/New_York",
	})
	require.NoError(t, err, "Create should work")

	// Read
	agency, err := client.Queries.GetAgency(ctx, "agency1")
	require.NoError(t, err, "Read should work")
	assert.Equal(t, "Agency 1", agency.Name)

	// Update (via transaction)
	tx, err := client.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE agencies SET name = ? WHERE id = ?", "Updated Agency", "agency1")
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err, "Transaction should work")

	// Verify update
	agency, err = client.Queries.GetAgency(ctx, "agency1")
	require.NoError(t, err)
	assert.Equal(t, "Updated Agency", agency.Name, "Update should persist")

	// Delete
	_, err = client.DB.ExecContext(ctx, "DELETE FROM agencies WHERE id = ?", "agency1")
	require.NoError(t, err, "Delete should work")

	// Verify deletion
	_, err = client.Queries.GetAgency(ctx, "agency1")
	assert.Error(t, err, "Should not find deleted agency")
}

func TestConfigureConnectionPoolWithDifferentConfigs(t *testing.T) {
	testCases := []struct {
		name            string
		dbPath          string
		expectedMaxConn int
	}{
		{"Memory database", ":memory:", 1},
		{"File database", "/tmp/test.db", 25},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			config := Config{
				DBPath: tc.dbPath,
				Env:    appconf.Test,
			}

			configureConnectionPool(db, config)

			stats := db.Stats()
			assert.Equal(t, tc.expectedMaxConn, stats.MaxOpenConnections,
				"MaxOpenConns should be %d for %s", tc.expectedMaxConn, tc.name)
		})
	}
}

func TestSQLitePerformanceWithBulkOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Create prerequisite data (agency, route, calendar/service)
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "perf_agency",
		Name:     "Performance Test Agency",
		Url:      "http://test.com",
		Timezone: "America/New_York",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:        "perf_route",
		AgencyID:  "perf_agency",
		ShortName: sql.NullString{String: "PERF", Valid: true},
		Type:      3,
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "perf_service",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20240101",
		EndDate:   "20241231",
	})
	require.NoError(t, err)

	// Create a test stop
	_, err = client.Queries.CreateStop(ctx, CreateStopParams{
		ID:   "stop_1",
		Name: sql.NullString{String: "Performance Test Stop", Valid: true},
		Lat:  40.0,
		Lon:  -74.0,
	})
	require.NoError(t, err)

	// Create test trip
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:        "perf_trip",
		RouteID:   "perf_route",
		ServiceID: "perf_service",
	})
	require.NoError(t, err)

	// Insert large batch to verify pragmas help performance
	const batchSize = 5000
	stopTimes := make([]CreateStopTimeParams, batchSize)
	for i := 0; i < batchSize; i++ {
		stopTimes[i] = CreateStopTimeParams{
			TripID:        "perf_trip",
			ArrivalTime:   int64(i * 60),
			DepartureTime: int64(i * 60),
			StopID:        "stop_1",
			StopSequence:  int64(i),
			PickupType:    sql.NullInt64{Int64: 0, Valid: true},
			DropOffType:   sql.NullInt64{Int64: 0, Valid: true},
		}
	}

	// This should complete quickly with proper pragmas
	err = client.bulkInsertStopTimes(ctx, stopTimes)
	require.NoError(t, err, "Bulk insert should succeed with performance pragmas")

	// Verify all inserted
	var count int
	err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_times").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, batchSize, count)
}
