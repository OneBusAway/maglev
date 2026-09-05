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

// TestForeignKeysEnabledOnEveryPooledConnection guards against foreign_keys being set on
// only the connection that runs migrations. foreign_keys is connection-scoped and
// non-persistent, so it must ride the DSN to reach every connection in the pool; holding
// several connections open at once (rather than reusing one via sequential Conn calls) is
// what actually exercises that.
func TestDSNAppendsForeignKeysWithCorrectSeparator(t *testing.T) {
	foreignKeyParam := "_foreign_keys=on"
	if DriverName == "sqlite" {
		foreignKeyParam = "_pragma=foreign_keys(1)"
	}

	assert.Equal(t, "test.db?"+foreignKeyParam, DSN("test.db"))
	assert.Equal(t, "file:test.db?cache=shared&"+foreignKeyParam, DSN("file:test.db?cache=shared"))
}

func TestForeignKeysEnabledOnEveryPooledConnection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "foreign_keys.db")
	client, err := NewClient(Config{DBPath: dbPath, Env: appconf.Development})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	const connCount = 5

	conns := make([]*sql.Conn, connCount)
	for i := range conns {
		conn, err := client.DB.Conn(ctx)
		require.NoError(t, err)
		conns[i] = conn
	}
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	for i, conn := range conns {
		var enabled int
		err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled)
		require.NoError(t, err)
		assert.Equal(t, 1, enabled, "connection %d should have foreign key enforcement on", i)
	}
}
