package gtfsdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestBackfillEmptyStopCodes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "empty-stop-codes.db")

	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)

	t.Cleanup(func() { _ = client.Close() })

	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, lat, lon) VALUES
			('legacy-empty-code', '', 47.6, -122.3),
			('existing-code', 'stop-code', 47.7, -122.4)
	`)
	require.NoError(t, err)
	require.NoError(t, client.Close())

	// Reopen through NewClient to exercise the startup backfill path used when an
	// unchanged feed would otherwise skip reimporting existing stop rows.
	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)

	var codeIsNull bool
	err = client.DB.QueryRowContext(ctx,
		"SELECT code IS NULL FROM stops WHERE id = 'legacy-empty-code'").Scan(&codeIsNull)
	require.NoError(t, err)
	assert.True(t, codeIsNull, "legacy empty stop codes should be normalized to NULL")

	var existingCode string
	err = client.DB.QueryRowContext(ctx,
		"SELECT code FROM stops WHERE id = 'existing-code'").Scan(&existingCode)
	require.NoError(t, err)
	assert.Equal(t, "stop-code", existingCode, "non-empty stop codes should be preserved")
}

func TestBackfillEmptyStopCodes_ErrorsWhenBackfillFails(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rejected-stop-code-backfill.db")

	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
	})

	// Simulate stop_code backfill error.
	_, err = client.DB.ExecContext(ctx, `
		INSERT INTO stops (id, code, lat, lon) VALUES ('legacy-empty-code', '', 47.6, -122.3);
		CREATE TRIGGER reject_empty_stop_code_backfill
		BEFORE UPDATE OF code ON stops
		WHEN OLD.code = ''
		BEGIN
			SELECT RAISE(ABORT, 'reject empty stop code backfill');
		END;
	`)
	require.NoError(t, err)
	require.NoError(t, client.Close())

	client, err = NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	assert.Nil(t, client, "client should be nil when the startup backfill fails")
	assert.ErrorContains(t, err, "failed to backfill empty stop codes")
}
