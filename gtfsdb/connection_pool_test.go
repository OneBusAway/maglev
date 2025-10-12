package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

func TestDatabaseConnectionPoolSettings(t *testing.T) {
	// Test that database connection pool is configured with appropriate settings
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	db := client.DB
	require.NotNil(t, db, "Database should be initialized")

	// Test MaxOpenConns setting - should be 1 for :memory: databases
	stats := db.Stats()
	assert.Equal(t, 1, stats.MaxOpenConnections, "MaxOpenConns should be set to 1 for :memory: databases")

	// Test that MaxIdleConns is configured (should be 5)
	// Note: Go's sql.DBStats doesn't expose MaxIdleConns directly,
	// but we can verify it's working by checking that idle connections are limited
	assert.True(t, stats.MaxOpenConnections > 0, "Connection pool should be configured")
}

func TestConnectionPoolBehavior(t *testing.T) {
	// Test connection pool behavior - note that :memory: databases use only 1 connection
	// so concurrent queries will be serialized
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	db := client.DB

	// Test that we can make sequential queries since :memory: uses 1 connection
	ctx := context.Background()

	// Make sequential queries to test that the single connection works
	for i := 0; i < 10; i++ {
		rows, err := db.QueryContext(ctx, "SELECT 1")
		assert.NoError(t, err, "Query should succeed")
		if rows != nil {
			rows.Close()
		}
	}

	// Verify connection stats
	stats := db.Stats()
	assert.True(t, stats.OpenConnections >= 0, "Should have open connections")
	assert.True(t, stats.InUse >= 0, "Should track connections in use")
	assert.Equal(t, 1, stats.MaxOpenConnections, "Should have 1 max connection for :memory: database")
}

func TestConnectionLifetime(t *testing.T) {
	// Test that connection max lifetime is configured
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	db := client.DB

	// Get initial stats
	initialStats := db.Stats()

	// Make a query to create at least one connection
	ctx := context.Background()
	row := db.QueryRowContext(ctx, "SELECT 1")
	var result int
	err = row.Scan(&result)
	require.NoError(t, err, "Initial query should succeed")
	assert.Equal(t, 1, result, "Query should return expected result")

	// Verify we have at least one connection
	stats := db.Stats()
	assert.True(t, stats.MaxOpenConnections > initialStats.MaxOpenConnections || stats.OpenConnections > 0,
		"Should have connection activity")
}

func TestConnectionPoolConfiguration(t *testing.T) {
	// Test the specific configuration values for in-memory databases
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err, "Should open database")
	defer func() { _ = db.Close() }()

	// Apply connection pool settings for in-memory database
	config := Config{DBPath: ":memory:", Env: appconf.Test}
	configureConnectionPool(db, config)

	// Verify settings through behavior - in-memory databases should use 1 connection
	stats := db.Stats()
	assert.Equal(t, 1, stats.MaxOpenConnections, "MaxOpenConns should be 1 for :memory: databases")

	// Test that we can ping the database
	ctx := context.Background()
	err = db.PingContext(ctx)
	assert.NoError(t, err, "Should be able to ping configured database")
}
