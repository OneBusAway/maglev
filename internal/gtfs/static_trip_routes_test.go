package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStaticTripRoutes(t *testing.T) {
	var cache staticTripRoutes
	tripIDs := []string{"T1", "T_ADDED"}

	routes, missing, generation := cache.lookup(tripIDs)
	assert.Empty(t, routes)
	assert.Equal(t, []string{"T1", "T_ADDED"}, missing)

	cache.store(map[string]string{"T1": "360"}, generation)
	routes, missing, _ = cache.lookup(tripIDs)
	assert.Equal(t, map[string]string{"T1": "360"}, routes)
	assert.Equal(t, []string{"T_ADDED"}, missing, "a trip absent from static data stays uncached")

	cache.Clear()
	routes, _, _ = cache.lookup(tripIDs)
	assert.Empty(t, routes, "a static reload can reassign trips to other routes")
}

// ReloadStatic replaces the DB before it clears the cache, so routes read
// from the old data can arrive after the clear.
func TestStaticTripRoutesIgnoresRoutesReadBeforeClear(t *testing.T) {
	var cache staticTripRoutes
	_, _, generation := cache.lookup([]string{"T1"})

	cache.Clear()
	cache.store(map[string]string{"T1": "360"}, generation)

	routes, _, _ := cache.lookup([]string{"T1"})
	assert.Empty(t, routes)
}
