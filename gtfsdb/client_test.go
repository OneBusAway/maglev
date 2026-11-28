package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestClient_PingAndStats verifies that Ping and Stats report accurate database
// liveness and pool metrics for active clients, and return appropriate errors or
// zero-values when invoked on a nil client or a client with a nil DB.
func TestClient_PingAndStats(t *testing.T) {
	client, err := NewClient(Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	require.NoError(t, client.Ping(ctx))

	stats := client.Stats()
	assert.GreaterOrEqual(t, stats.OpenConnections, 1)

	emptyClient := &Client{}
	assert.Error(t, emptyClient.Ping(ctx))
	assert.Equal(t, 0, emptyClient.Stats().OpenConnections)

	var nilClient *Client
	assert.Error(t, nilClient.Ping(ctx))
	assert.Equal(t, 0, nilClient.Stats().OpenConnections)
}
