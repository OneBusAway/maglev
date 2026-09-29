package restapi

import (
	"sync"
	"sync/atomic"
)

// maxBlockTripCacheEntries caps how many trips the cache keeps. An entry is
// dominated by the trip's stop times and shape points, on the order of a few KB
// for a typical trip, so this holds the busiest blocks without letting a feed
// with tens of thousands of trips grow unbounded. Past the cap we stop inserting
// and keep serving what is already there.
const maxBlockTripCacheEntries = 2000

// blockTripDataCache caches loadBlockTripData results across requests.
//
// What it stores comes only from static GTFS: a trip's stop times, its shape
// points, and the cumulative distances derived from them. None of that depends
// on the request's time or on any realtime feed, which is what makes it safe to
// reuse between requests. The snapshotCache in scheduled_block_helper.go is a
// different thing and stays as it is: its key includes currentTime, so it can
// only ever help within one request.
//
// Each entry records the static generation it was built from, and get treats a
// mismatch as a miss. Correctness therefore does not depend on the reload path
// clearing anything, which matters because a writer can be midway through put
// when a reload lands.
//
// The zero value is ready to use.
type blockTripDataCache struct {
	entries    sync.Map // tripID -> blockTripCacheEntry
	generation atomic.Uint64
	// count bounds the cache against maxBlockTripCacheEntries. sync.Map has no
	// length, so this tracks it separately and can drift by the number of puts
	// racing a reset. It only decides whether to admit more entries, so a small
	// drift costs a few cache slots and nothing else.
	count atomic.Int64
}

type blockTripCacheEntry struct {
	generation uint64
	data       blockTripData
}

// get returns the entry for tripID if it was built from generation.
func (c *blockTripDataCache) get(generation uint64, tripID string) (blockTripData, bool) {
	value, ok := c.entries.Load(tripID)
	if !ok {
		return blockTripData{}, false
	}
	entry, ok := value.(blockTripCacheEntry)
	if !ok || entry.generation != generation {
		return blockTripData{}, false
	}
	return entry.data, true
}

// put stores data for tripID unless the cache is already full.
func (c *blockTripDataCache) put(generation uint64, tripID string, data blockTripData) {
	// Sync first so the cache does not depend on a caller having reset it. A fresh
	// cache sits at generation 0, and without this the first put at a non-zero
	// generation would be thrown away by the next reset.
	c.reset(generation)
	// A request that read an older generation can still be in flight when a reload
	// lands. What it loaded describes a dataset we have already dropped, so storing
	// it would only spend a slot on something get will refuse.
	if generation < c.generation.Load() {
		return
	}
	if c.count.Load() >= maxBlockTripCacheEntries {
		return
	}
	entry := blockTripCacheEntry{generation: generation, data: data}
	if _, replaced := c.entries.Swap(tripID, entry); !replaced {
		c.count.Add(1)
	}
}

// reset drops everything when the static dataset has been replaced. get already
// refuses entries from an older generation, so this is only about releasing the
// memory the previous dataset's entries still hold.
//
// The generation only ever moves forward. A request that read an older generation
// can still call in after a reload, and letting it move the cache back would clear
// entries that describe the current dataset and leave the next request to clear
// again.
func (c *blockTripDataCache) reset(generation uint64) {
	for {
		previous := c.generation.Load()
		if generation <= previous {
			return
		}
		// Whichever caller wins the swap does the clearing, so a burst of concurrent
		// requests after a reload does not clear repeatedly.
		if c.generation.CompareAndSwap(previous, generation) {
			c.entries.Clear()
			c.count.Store(0)
			return
		}
	}
}

// staticGeneration reads the manager's static generation, tolerating the nil
// manager that some tests construct.
func (api *RestAPI) staticGeneration() uint64 {
	if api.GtfsManager == nil {
		return 0
	}
	return api.GtfsManager.StaticGeneration()
}
