package gtfsdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestNewClient_InvalidConfigHandling(t *testing.T) {
	// Test that NewClient returns an error instead of calling log.Fatal
	// when configuration is invalid (test env with file DB)
	config := Config{
		DBPath:  "/tmp/invalid_test_db.sqlite",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	assert.Error(t, err, "NewClient should return error for invalid test config")
	assert.Nil(t, client, "Client should be nil when creation fails")
	assert.Contains(t, err.Error(), "test database must use in-memory storage", "Error should mention in-memory requirement")
}

func TestNewClient_ValidConfig(t *testing.T) {
	// Test that NewClient works correctly with valid configuration
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
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
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
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
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	// Test with invalid GTFS data
	invalidData := []byte("invalid gtfs data")
	err = client.processAndStoreGTFSDataWithSource(invalidData, "test-source")
	assert.Error(t, err, "processAndStoreGTFSDataWithSource should return error for invalid data")

	// Test with empty data
	emptyData := []byte{}
	err = client.processAndStoreGTFSDataWithSource(emptyData, "test-source")
	assert.Error(t, err, "processAndStoreGTFSDataWithSource should return error for empty data")
}

func TestImportFromFile_ErrorHandling(t *testing.T) {
	// Create a client with a valid config
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Test with non-existent file
	err = client.ImportFromFile(ctx, "/nonexistent/file.zip")
	assert.Error(t, err, "ImportFromFile should return error for non-existent file")
}

func TestDownloadAndStore_ErrorHandling(t *testing.T) {
	// Create a client with a valid config
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Test with invalid URL
	err = client.DownloadAndStore(ctx, "invalid-url", "", "")
	assert.Error(t, err, "DownloadAndStore should return error for invalid URL")

	// Test with non-existent URL
	err = client.DownloadAndStore(ctx, "http://nonexistent.example.com/data.zip", "", "")
	assert.Error(t, err, "DownloadAndStore should return error for non-existent URL")
}

func TestDownloadAndStore_AuthenticationHeaders(t *testing.T) {
	// Track whether the auth header was received
	headerReceived := false
	expectedHeaderName := "X-API-Key"
	expectedHeaderValue := "test-secret-key-123"

	// Create a test HTTP server that checks for auth headers
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the auth header is present and correct
		actualValue := r.Header.Get(expectedHeaderName)
		if actualValue == expectedHeaderValue {
			headerReceived = true
		}

		// Return a minimal valid GTFS zip file
		// (We'll return an error since we don't need to test the full import here)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid-zip-data"))
	}))
	defer server.Close()

	// Create a client with a valid config
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Test with authentication headers
	_ = client.DownloadAndStore(ctx, server.URL, expectedHeaderName, expectedHeaderValue)

	// Verify the auth header was sent
	assert.True(t, headerReceived, "Authentication header should be sent in HTTP request")
}

func TestDownloadAndStore_NoAuthHeaders(t *testing.T) {
	// Track whether any auth header was received
	authHeaderReceived := false

	// Create a test HTTP server that checks for auth headers
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if any Authorization-like header is present
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" {
			authHeaderReceived = true
		}

		// Return invalid data
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid-zip-data"))
	}))
	defer server.Close()

	// Create a client with a valid config
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: false,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "NewClient should succeed")
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Test without authentication headers (empty strings)
	_ = client.DownloadAndStore(ctx, server.URL, "", "")

	// Verify no auth header was sent
	assert.False(t, authHeaderReceived, "No authentication header should be sent when not configured")
}
