package gtfs

import (
	"maps"
	"sync"
)

// staticTripRoutes caches, across refreshes and feeds, the static route of
// each trip a realtime feed has named. Feeds can name routes the static GTFS lacks (Tampa's GTFS-RT feed
// reports static route "360" as "360LX"), while legacy exports always used
// the static trip's route. Trips missing from static data are not cached,
// so the cache holds at most one entry per static trip.
type staticTripRoutes struct {
	mu     sync.RWMutex
	routes map[string]string // trip ID -> static route ID
	// generation counts Clear calls, so routes read before a static reload
	// are not stored after it.
	generation uint64
}

// lookup returns the cached routes of tripIDs, the trip IDs that have no
// cached route, and the cache generation to pass to store with the routes
// read for them.
func (cache *staticTripRoutes) lookup(tripIDs []string) (map[string]string, []string, uint64) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	routes := make(map[string]string, len(tripIDs))
	var missing []string
	for _, tripID := range tripIDs {
		if routeID, ok := cache.routes[tripID]; ok {
			routes[tripID] = routeID
		} else {
			missing = append(missing, tripID)
		}
	}
	return routes, missing, cache.generation
}

// store caches routes, keyed by trip ID, unless the cache was cleared after
// generation was read, since they may then come from replaced static data.
func (cache *staticTripRoutes) store(routes map[string]string, generation uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if generation != cache.generation {
		return
	}
	if cache.routes == nil {
		cache.routes = make(map[string]string, len(routes))
	}
	maps.Copy(cache.routes, routes)
}

// Clear drops every cached route; static data has changed.
func (cache *staticTripRoutes) Clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.routes = nil
	cache.generation++
}
