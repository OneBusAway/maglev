package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
)

func TestStaticTripRoutes(t *testing.T) {
	var cache staticTripRoutes
	tripIDs := map[string]struct{}{"T1": {}, "T_ADDED": {}}

	assert.ElementsMatch(t, []string{"T1", "T_ADDED"}, cache.missing(tripIDs))

	cache.store([]gtfsdb.Trip{{ID: "T1", RouteID: "360"}})
	assert.Equal(t, "360", cache.routeOf("T1"))
	assert.Equal(t, []string{"T_ADDED"}, cache.missing(tripIDs), "a trip absent from static data stays uncached")

	cache.Clear()
	assert.Empty(t, cache.routeOf("T1"), "a static reload can reassign trips to other routes")
}
