package gtfsdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// seedLegacyBlockRows inserts legacy rows on a dedicated connection with foreign
// keys off, since these tests only care about block_id normalization.
func seedLegacyBlockRows(ctx context.Context, t *testing.T, client *Client, extraSQL string) {
	t.Helper()

	conn, err := client.DB.Conn(ctx)
	require.NoError(t, err)

	_, err = conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	require.NoError(t, err)

	_, err = conn.ExecContext(ctx, `
		INSERT INTO trips (id, block_id, route_id, service_id) VALUES
			('legacy-trip', '', 'route1', 'service1'),
			('valid-trip', 'block1', 'route1', 'service1');
		INSERT INTO block_trip_entry (block_trip_index_id, trip_id, block_id, service_id, block_trip_sequence) VALUES
			(1, 'legacy-trip', '', 'service1', 1),
			(1, 'valid-trip', 'block1', 'service1', 2);
	`+extraSQL)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestBackfillEmptyBlockIDs(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "empty-block-ids.db")

	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	seedLegacyBlockRows(ctx, t, client, "")
	require.NoError(t, client.Close())

	// Reopen through NewClient to exercise the startup backfill path.
	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)

	var tripBlockIDIsNull bool
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT block_id IS NULL FROM trips WHERE id = 'legacy-trip'").Scan(&tripBlockIDIsNull))
	assert.True(t, tripBlockIDIsNull, "legacy empty trip block IDs should be normalized to NULL")

	var entryBlockIDIsNull bool
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT block_id IS NULL FROM block_trip_entry WHERE trip_id = 'legacy-trip'").Scan(&entryBlockIDIsNull))
	assert.True(t, entryBlockIDIsNull, "legacy empty block_trip_entry block IDs should be normalized to NULL")

	var validTripBlockID, validEntryBlockID string
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT block_id FROM trips WHERE id = 'valid-trip'").Scan(&validTripBlockID))
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT block_id FROM block_trip_entry WHERE trip_id = 'valid-trip'").Scan(&validEntryBlockID))
	assert.Equal(t, "block1", validTripBlockID, "non-empty trip block IDs should be preserved")
	assert.Equal(t, "block1", validEntryBlockID, "non-empty entry block IDs should be preserved")
}

func TestBackfillEmptyBlockIDs_RollsBackWhenSecondUpdateFails(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rejected-block-id-backfill.db")

	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
	})

	// The trigger rejects the second update (block_trip_entry), after the trips
	// update has already run inside the same transaction.
	seedLegacyBlockRows(ctx, t, client, `
		CREATE TRIGGER reject_empty_block_id_backfill
		BEFORE UPDATE OF block_id ON block_trip_entry
		WHEN OLD.block_id = ''
		BEGIN
			SELECT RAISE(ABORT, 'reject empty block id backfill');
		END;
	`)
	require.NoError(t, client.Close())

	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	assert.Nil(t, client, "client should be nil when the startup backfill fails")
	assert.ErrorContains(t, err, "unable to backfill empty block IDs")
	assert.ErrorContains(t, err, "block_trip_entry")

	// The trips update must have been rolled back with the failed second update.
	db, err := sql.Open(DriverName, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var blockID sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT block_id FROM trips WHERE id = 'legacy-trip'").Scan(&blockID))
	assert.True(t, blockID.Valid, "trips update should roll back when block_trip_entry update fails")
	assert.Equal(t, "", blockID.String)
}

func TestBackfillEmptyBlockIDs_ErrorsWhenTripsUpdateFails(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rejected-trips-block-id-backfill.db")

	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
	})

	seedLegacyBlockRows(ctx, t, client, `
		CREATE TRIGGER reject_trips_block_id_backfill
		BEFORE UPDATE OF block_id ON trips
		WHEN OLD.block_id = ''
		BEGIN
			SELECT RAISE(ABORT, 'reject trips block id backfill');
		END;
	`)
	require.NoError(t, client.Close())

	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	assert.Nil(t, client, "client should be nil when the startup backfill fails")
	assert.ErrorContains(t, err, "unable to backfill empty block IDs")
	assert.ErrorContains(t, err, "trips block_id")

	db, err := sql.Open(DriverName, dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var entryBlockID sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT block_id FROM block_trip_entry WHERE trip_id = 'legacy-trip'").Scan(&entryBlockID))
	assert.True(t, entryBlockID.Valid, "block_trip_entry should be untouched when the trips update fails")
	assert.Equal(t, "", entryBlockID.String)
}
