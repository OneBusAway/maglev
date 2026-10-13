package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
)

func TestStaticTripRoutes(t *testing.T) {
	var cache staticTripRoutes
	tripIDs := map[string]struct{}{"T1": {}, "T_ADDED": {}}

	missing, generation := cache.missing(tripIDs)
	assert.ElementsMatch(t, []string{"T1", "T_ADDED"}, missing)

	cache.store([]gtfsdb.Trip{{ID: "T1", RouteID: "360"}}, generation)
	assert.Equal(t, "360", cache.routeOf("T1"))
	missing, _ = cache.missing(tripIDs)
	assert.Equal(t, []string{"T_ADDED"}, missing, "a trip absent from static data stays uncached")

	cache.Clear()
	assert.Empty(t, cache.routeOf("T1"), "a static reload can reassign trips to other routes")
}

// ReloadStatic replaces the DB before it clears the cache, so routes read
// from the old data can arrive after the clear.
func TestStaticTripRoutesIgnoresRoutesReadBeforeClear(t *testing.T) {
	var cache staticTripRoutes
	_, generation := cache.missing(map[string]struct{}{"T1": {}})

	cache.Clear()
	cache.store([]gtfsdb.Trip{{ID: "T1", RouteID: "360"}}, generation)

	assert.Empty(t, cache.routeOf("T1"))
}
