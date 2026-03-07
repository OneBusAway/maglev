package gtfs

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
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
	assert.Empty(t, m.GetRoutes())
	m.RUnlock()

	assert.Empty(t, m.GetAllTripUpdates())

	m.RLock()
	m.PrintStatistics() // Ensure no panic
	m.RUnlock()
}
