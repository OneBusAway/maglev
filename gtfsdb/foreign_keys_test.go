package gtfsdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestDSNAppendsConnectionSettingsWithCorrectSeparator pins the exact
// connection settings that each driver's DSN adds, and checks that they join a
// path that already has a query with "&" instead of "?".
func TestDSNAppendsConnectionSettingsWithCorrectSeparator(t *testing.T) {
	params := "_foreign_keys=on&_cache_size=-2000"
	if DriverName == "sqlite" {
		params = "_pragma=foreign_keys(1)&_pragma=cache_size(-2000)&_pragma=busy_timeout(" + strconv.Itoa(connectionBusyTimeoutMs) + ")"
	}

	assert.Equal(t, "test.db?"+params, DSN("test.db"))
	assert.Equal(t, "file:test.db?cache=shared&"+params, DSN("file:test.db?cache=shared"))
}

// TestForeignKeysEnabledOnEveryPooledConnection guards against foreign_keys being set on
// only the connection that runs migrations. foreign_keys is connection-scoped and
// non-persistent, so it must ride the DSN to reach every connection in the pool; holding
// several connections open at once (rather than reusing one via sequential Conn calls) is
// what actually exercises that.
func TestForeignKeysEnabledOnEveryPooledConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "foreign_keys.db")
	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	const connCount = 5

	for i, conn := range holdConnections(t, client.DB, connCount) {
		var enabled int
		err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled)
		require.NoError(t, err)
		assert.Equal(t, 1, enabled, "connection %d should have foreign key enforcement on", i)
	}
}

// TestDSNWithExistingQueryOpens checks that a DSN built from a path that
// already has a query opens a database with the connection settings applied,
// so the "&" separator yields a DSN the driver honours.
func TestDSNWithExistingQueryOpens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "existing_query.db")
	db, err := sql.Open(DriverName, DSN("file:"+dbPath+"?cache=private"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	var foreignKeys, busyTimeout int
	require.NoError(t, db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys))
	require.NoError(t, db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))

	assert.Equal(t, 1, foreignKeys)
	assert.Equal(t, connectionBusyTimeoutMs, busyTimeout)
}

// holdConnections opens count connections from db and keeps all of them open
// until the test ends. Holding them at once, instead of reusing one through
// sequential Conn calls, makes the pool open a separate connection for each.
func holdConnections(t *testing.T, db *sql.DB, count int) []*sql.Conn {
	t.Helper()
	conns := make([]*sql.Conn, count)
	for i := range conns {
		conn, err := db.Conn(context.Background())
		require.NoError(t, err)
		conns[i] = conn
	}
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return conns
}
