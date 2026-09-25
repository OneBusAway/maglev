package gtfsdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestFlexSchema_TablesExist(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	for _, table := range []string{
		"locations", "location_groups", "location_group_stops", "booking_rules",
		"flex_stop_times", "ondemand_services", "ondemand_rules", "ondemand_stop_services",
	} {
		var n int
		require.NoError(t, client.DB.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n), table)
		assert.Equal(t, 1, n, "table %s should exist", table)
	}

	counts, err := client.TableCounts()
	require.NoError(t, err)
	for _, table := range []string{"locations", "location_groups", "location_group_stops", "booking_rules",
		"flex_stop_times", "ondemand_services", "ondemand_rules", "ondemand_stop_services"} {
		_, ok := counts[table]
		assert.True(t, ok, "TableCounts should report %s", table)
	}
}

func TestFlexSchema_FlexStopTimeRequiresExactlyOneReference(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var tripID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT id FROM trips LIMIT 1").Scan(&tripID))

	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO flex_stop_times (trip_id, stop_sequence, stop_id, location_id, location_group_id,
			start_pickup_drop_off_window, end_pickup_drop_off_window, pickup_type, drop_off_type)
		VALUES (?, 900, NULL, 'zone', 'group', 0, 3600, 2, 1)`, tripID)
	assert.Error(t, err, "two references must violate the CHECK constraint")

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO flex_stop_times (trip_id, stop_sequence, stop_id, location_id, location_group_id,
			start_pickup_drop_off_window, end_pickup_drop_off_window, pickup_type, drop_off_type)
		VALUES (?, 901, NULL, 'zone', NULL, 0, 3600, 2, 1)`, tripID)
	assert.NoError(t, err)
}

func TestNewClient_InvalidatesPreFlexImportOnce(t *testing.T) {
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

	// Stand in for a database written before the flex tables existed.
	_, err = client.DB.ExecContext(ctx, "PRAGMA user_version = 0")
	require.NoError(t, err)
	require.NoError(t, client.Close())

	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	metadata, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, "invalidated:flex-import-v1", metadata.FileHash)

	var version int
	require.NoError(t, client.DB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version))
	assert.Equal(t, flexImportVersion, version)

	// The sentinel keeps hasExisting=true so the next import clears old rows before
	// inserting and does not collide on the stop_times primary key.
	changed, err := client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)
	assert.True(t, changed, "the sentinel hash must force a reimport")

	metadata, err = client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, parsed.Hash, metadata.FileHash)

	// Reopening at the current version must not invalidate again.
	require.NoError(t, client.Close())
	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	metadata, err = client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, parsed.Hash, metadata.FileHash)
}
