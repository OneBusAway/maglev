package gtfsdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestNewClient_InvalidConfigHandling(t *testing.T) {
	// Test that NewClient returns an error instead of calling log.Fatal
	// when configuration is invalid (test env with file DB)
	config := Config{
		DBPath: "/tmp/invalid_test_db.sqlite",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	assert.Error(t, err, "NewClient should return error for invalid test config")
	assert.Nil(t, client, "Client should be nil when creation fails")
	assert.Contains(t, err.Error(), "test database must use in-memory storage", "Error should mention in-memory requirement")
}

func TestNewClient_ValidConfig(t *testing.T) {
	// Test that NewClient works correctly with valid configuration
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed with valid config")
	require.NotNil(t, client, "Client should not be nil")
	defer func() { _ = client.Close() }()

	// Verify the client is functional
	assert.NotNil(t, client.DB, "Database should be initialized")
	assert.NotNil(t, client.Queries, "Queries should be initialized")
}

func TestTableCounts_ErrorHandling(t *testing.T) {
	// Create a client with a valid config
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	// Test TableCounts with a valid database
	counts, err := client.TableCounts()
	require.NoError(t, err, "TableCounts should succeed with valid database")
	assert.NotNil(t, counts, "Counts should not be nil")
	assert.IsType(t, map[string]int{}, counts, "Counts should be a map")
}

func TestProcessAndStoreGTFSData_ErrorHandling(t *testing.T) {
	// Create a client with a valid config
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	// Test with invalid GTFS data
	invalidData := []byte("invalid gtfs data")
	_, err = ParseGtfsData(invalidData, "test-source")
	assert.Error(t, err, "ParseGtfsData should return error for invalid data")

	// Test with empty data
	emptyData := []byte{}
	_, err = ParseGtfsData(emptyData, "test-source")
	assert.Error(t, err, "ParseGtfsData should return error for empty data")
}
