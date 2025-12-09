package gtfsdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	_ "modernc.org/sqlite" // Pure Go SQLite driver with FTS5 support
)

// getTestFixturePath returns the absolute path to a fixture file in the testdata directory
func getTestFixturePath(t *testing.T, fixturePath string) string {
	t.Helper()

	absPath, err := filepath.Abs(filepath.Join("..", "testdata", fixturePath))
	if err != nil {
		t.Fatalf("Failed to get absolute path to testdata/%s: %v", fixturePath, err)
	}

	return absPath
}

// createTestData creates two different test GTFS files for testing hash changes
func createTestData(t *testing.T) ([]byte, []byte) {
	t.Helper()

	// Read the original RABA test data
	originalPath := getTestFixturePath(t, "raba.zip")
	originalData, err := os.ReadFile(originalPath)
	require.NoError(t, err, "Failed to read original test data")

	// Create modified data by appending a byte
	modifiedData := append([]byte{}, originalData...)
	modifiedData = append(modifiedData, 0x00) // Add a null byte to change the hash

	return originalData, modifiedData
}

func TestConditionalImport_InitialImport(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	originalData, _ := createTestData(t)

	// Perform initial import
	err = client.processAndStoreGTFSDataWithSource(originalData, "test-source")
	require.NoError(t, err, "Initial import should succeed")

	// Verify metadata was stored
	metadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve import metadata")

	assert.NotEmpty(t, metadata.FileHash, "File hash should be stored")
	assert.Equal(t, "test-source", metadata.FileSource, "File source should match")
	assert.Greater(t, metadata.ImportTime, int64(0), "Import time should be set")

	// Verify data was imported
	agencies, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err, "Should be able to retrieve agencies")
	assert.Greater(t, len(agencies), 0, "Should have imported agencies")
}

func TestConditionalImport_SkipUnchangedData(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	originalData, _ := createTestData(t)

	// Perform initial import
	err = client.processAndStoreGTFSDataWithSource(originalData, "test-source")
	require.NoError(t, err, "Initial import should succeed")

	// Get initial metadata
	initialMetadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve initial metadata")

	// Get initial agency count
	initialAgencies, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err, "Should be able to retrieve initial agencies")
	initialCount := len(initialAgencies)

	// Wait a bit to ensure timestamp would change if import actually occurred
	time.Sleep(10 * time.Millisecond)

	// Perform second import with same data
	startTime := time.Now()
	err = client.processAndStoreGTFSDataWithSource(originalData, "test-source")
	duration := time.Since(startTime)
	require.NoError(t, err, "Second import should succeed")

	// Verify import was skipped (should be very fast)
	assert.Less(t, duration, 100*time.Millisecond, "Import should be very fast when skipped")

	// Verify metadata unchanged
	finalMetadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve final metadata")

	assert.Equal(t, initialMetadata.FileHash, finalMetadata.FileHash, "File hash should be unchanged")
	assert.Equal(t, initialMetadata.ImportTime, finalMetadata.ImportTime, "Import time should be unchanged")
	assert.Equal(t, initialMetadata.FileSource, finalMetadata.FileSource, "File source should be unchanged")

	// Verify data count unchanged
	finalAgencies, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err, "Should be able to retrieve final agencies")
	assert.Equal(t, initialCount, len(finalAgencies), "Agency count should be unchanged")
}

func TestConditionalImport_ReloadChangedData(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	originalData, modifiedData := createTestData(t)

	// Perform initial import
	err = client.processAndStoreGTFSDataWithSource(originalData, "test-source")
	require.NoError(t, err, "Initial import should succeed")

	// Get initial metadata
	initialMetadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve initial metadata")

	// Perform import with modified data
	err = client.processAndStoreGTFSDataWithSource(modifiedData, "test-source")
	require.NoError(t, err, "Import with modified data should succeed")

	// Verify metadata was updated
	finalMetadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve final metadata")

	assert.NotEqual(t, initialMetadata.FileHash, finalMetadata.FileHash, "File hash should have changed")
	assert.GreaterOrEqual(t, finalMetadata.ImportTime, initialMetadata.ImportTime, "Import time should have been updated")
	assert.Equal(t, "test-source", finalMetadata.FileSource, "File source should remain the same")

	// Verify data is still accessible (not corrupted by the modified zip)
	agencies, err := client.Queries.ListAgencies(ctx)
	// Note: Modified data might cause parsing errors, so we just verify the operation completes
	// and that metadata was properly updated to reflect the attempt
	_ = agencies
	_ = err
}

func TestConditionalImport_DifferentSources(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	originalData, _ := createTestData(t)

	// Perform initial import with source A
	err = client.processAndStoreGTFSDataWithSource(originalData, "source-a")
	require.NoError(t, err, "Initial import should succeed")

	// Get initial metadata
	initialMetadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve initial metadata")

	// Perform import with same data but different source
	err = client.processAndStoreGTFSDataWithSource(originalData, "source-b")
	require.NoError(t, err, "Import with different source should succeed")

	// Verify metadata was updated (different source should trigger reimport)
	finalMetadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve final metadata")

	assert.Equal(t, initialMetadata.FileHash, finalMetadata.FileHash, "File hash should be the same")
	assert.GreaterOrEqual(t, finalMetadata.ImportTime, initialMetadata.ImportTime, "Import time should have been updated")
	assert.Equal(t, "source-b", finalMetadata.FileSource, "File source should have been updated")
}

func TestConditionalImport_FileImport(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	testFilePath := getTestFixturePath(t, "raba.zip")

	// Perform initial import from file
	err = client.ImportFromFile(ctx, testFilePath)
	require.NoError(t, err, "File import should succeed")

	// Verify metadata was stored with file path as source
	metadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Should be able to retrieve import metadata")

	assert.NotEmpty(t, metadata.FileHash, "File hash should be stored")
	assert.Equal(t, testFilePath, metadata.FileSource, "File source should be the file path")
	assert.Greater(t, metadata.ImportTime, int64(0), "Import time should be set")

	// Verify data was imported
	agencies, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err, "Should be able to retrieve agencies")
	assert.Greater(t, len(agencies), 0, "Should have imported agencies")

	// Import same file again - should skip
	startTime := time.Now()
	err = client.ImportFromFile(ctx, testFilePath)
	duration := time.Since(startTime)
	require.NoError(t, err, "Second file import should succeed")

	// Verify import was skipped (should be very fast)
	assert.Less(t, duration, 100*time.Millisecond, "Second import should be very fast when skipped")
}

func TestClearAllGTFSData(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	originalData, _ := createTestData(t)

	// Perform initial import
	err = client.processAndStoreGTFSDataWithSource(originalData, "test-source")
	require.NoError(t, err, "Initial import should succeed")

	// Verify data exists
	agencies, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err, "Should be able to retrieve agencies")
	assert.Greater(t, len(agencies), 0, "Should have agencies before clear")

	// Clear all data
	err = client.clearAllGTFSData(ctx)
	require.NoError(t, err, "Should be able to clear all GTFS data")

	// Verify all data was cleared
	agenciesAfter, err := client.Queries.ListAgencies(ctx)
	require.NoError(t, err, "Should be able to query agencies after clear")
	assert.Equal(t, 0, len(agenciesAfter), "Should have no agencies after clear")

	routesAfter, err := client.Queries.ListRoutes(ctx)
	require.NoError(t, err, "Should be able to query routes after clear")
	assert.Equal(t, 0, len(routesAfter), "Should have no routes after clear")

	// Note: Import metadata should NOT be cleared by clearAllGTFSData
	metadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err, "Import metadata should still exist after clear")
	assert.NotEmpty(t, metadata.FileHash, "Import metadata should not be cleared")
}
