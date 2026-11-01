package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
