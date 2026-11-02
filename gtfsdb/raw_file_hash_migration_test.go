package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/internal/appconf"
)

// TestEnsureRawFileHashColumnAddsMissingColumn covers a database created
// before import_metadata had raw_file_hash: the column must be added, and
// adding it again must be a no-op.
func TestEnsureRawFileHashColumnAddsMissingColumn(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open(DriverName, ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.ExecContext(ctx, `CREATE TABLE import_metadata (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		file_hash TEXT NOT NULL,
		import_time INTEGER NOT NULL,
		file_source TEXT NOT NULL,
		feed_expires_at INTEGER
	) STRICT`)
	require.NoError(t, err)

	require.NoError(t, ensureRawFileHashColumn(ctx, db))
	require.NoError(t, ensureRawFileHashColumn(ctx, db), "a second call must not fail")

	var present int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('import_metadata') WHERE name = 'raw_file_hash'",
	).Scan(&present)
	require.NoError(t, err)
	assert.Equal(t, 1, present)
}

// TestStoreGtfsDataRawHashOnlyChangeKeepsImportedData checks that a change to
// the raw hash alone updates raw_file_hash without reporting a new import, so
// the imported tables are left alone.
func TestStoreGtfsDataRawHashOnlyChangeKeepsImportedData(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	defer client.Close()

	_, err = client.Queries.UpsertImportMetadata(ctx, UpsertImportMetadataParams{
		FileHash:    "abcdef0123",
		ImportTime:  1,
		FileSource:  "feed.zip",
		RawFileHash: "oldraw0123",
	})
	require.NoError(t, err)

	imported, err := client.StoreGtfsData(ctx, &GtfsData{
		Hash:    "abcdef0123",
		RawHash: "newraw0123",
		Source:  "feed.zip",
	})
	require.NoError(t, err)
	assert.False(t, imported, "a raw-only change must not trigger a reimport")

	md, err := client.Queries.GetImportMetadata(ctx)
	require.NoError(t, err)
	assert.Equal(t, "newraw0123", md.RawFileHash)
	assert.Equal(t, "abcdef0123", md.FileHash)
}
