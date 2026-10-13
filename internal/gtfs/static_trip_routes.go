package gtfs

import (
	"sync"

	"maglev.onebusaway.org/gtfsdb"
)

// staticTripRoutes caches the static route of each trip a realtime feed has
// named. Feeds can name routes the static GTFS lacks (Tampa's GTFS-RT feed
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

// routeOf returns tripID's static route, or "" when it is not cached.
func (cache *staticTripRoutes) routeOf(tripID string) string {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.routes[tripID]
}

// missing returns the trip IDs that have no cached route, and the cache
// generation to pass to store with the routes read for them.
func (cache *staticTripRoutes) missing(tripIDs map[string]struct{}) ([]string, uint64) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	var missing []string
	for tripID := range tripIDs {
		if _, ok := cache.routes[tripID]; !ok {
			missing = append(missing, tripID)
		}
	}
	return missing, cache.generation
}

// store caches the trips' routes unless the cache was cleared after
// generation was read, since they may then come from replaced static data.
func (cache *staticTripRoutes) store(trips []gtfsdb.Trip, generation uint64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if generation != cache.generation {
		return
	}
	if cache.routes == nil {
		cache.routes = make(map[string]string, len(trips))
	}
	for _, trip := range trips {
		cache.routes[trip.ID] = trip.RouteID
	}
}

// Clear drops every cached route; static data has changed.
func (cache *staticTripRoutes) Clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.routes = nil
	cache.generation++
}
