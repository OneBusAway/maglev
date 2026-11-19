//go:build !purego

package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// SQLite only tracks memory use while memory status is on, so a client that
// has opened a database and run a query reporting zero shows it is off.
func TestSQLiteMemoryStatusDisabled(t *testing.T) {
	require.NoError(t, disableMemoryStatusErr)

	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.Queries.GetActiveServiceIDsForDate(context.Background(), "20250613")
	require.NoError(t, err)

	assert.Zero(t, sqliteMemoryUsed())
}

// SQLite rejects configuration once it has initialized, so a call after a
// client has opened reports the failure instead of succeeding silently.
func TestDisableMemoryStatusAfterOpenFails(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	err = disableMemoryStatus()

	assert.ErrorContains(t, err, "returned 21")
}
