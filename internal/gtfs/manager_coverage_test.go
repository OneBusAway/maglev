package gtfs

import (
	"context"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerAccessorsCoverage(t *testing.T) {
	m := &Manager{
		systemETag: "tag1",
		gtfsData:   &gtfs.Static{},
	}

	m.MarkReady()
	assert.True(t, m.IsReady())
	assert.Equal(t, "tag1", m.GetSystemETag())

	m.RLock()
	assert.NotNil(t, m.GetStaticData())
	assert.Empty(t, m.GetStops())
	assert.Empty(t, m.GetBlockLayoverIndicesForRoute("r"))
	routes, err := m.GetRoutes(context.Background())
	require.NoError(t, err)
	assert.Empty(t, routes)
	m.RUnlock()

	assert.Empty(t, m.GetAllTripUpdates())

	m.RLock()
	m.PrintStatistics() // Ensure no panic
	m.RUnlock()
}
