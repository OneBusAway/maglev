package restapi

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockTripDataCache_GenerationMismatchIsMiss(t *testing.T) {
	var cache blockTripDataCache
	cache.put(7, "trip-1", blockTripData{id: "trip-1"})

	data, ok := cache.get(7, "trip-1")
	require.True(t, ok, "same generation should hit")
	assert.Equal(t, "trip-1", data.id)

	// A reload bumps the generation. The entry is still in the map, because reset
	// may not have run yet, and it must not be served anyway.
	_, ok = cache.get(8, "trip-1")
	assert.False(t, ok, "an entry from an older dataset must not be served")
}

func TestBlockTripDataCache_ResetClearsEntries(t *testing.T) {
	var cache blockTripDataCache
	cache.put(1, "trip-1", blockTripData{id: "trip-1"})

	cache.reset(1)
	_, ok := cache.get(1, "trip-1")
	assert.True(t, ok, "reset to the same generation should keep entries")

	cache.reset(2)
	_, ok = cache.get(2, "trip-1")
	assert.False(t, ok, "reset to a new generation should drop entries")
	assert.Zero(t, cache.count.Load(), "count should be zeroed so the cache can refill")
}

// A request that read the old generation can still be in flight when a reload
// lands. It must not drag the cache back to that generation, or it would clear
// entries built for the current dataset and leave the next request to clear again.
func TestBlockTripDataCache_GenerationOnlyMovesForward(t *testing.T) {
	var cache blockTripDataCache

	// A request reads generation 1 and warms the cache.
	cache.reset(1)
	cache.put(1, "trip-1", blockTripData{id: "trip-1"})

	// A reload lands and a newer request moves the cache on.
	cache.reset(2)
	cache.put(2, "trip-2", blockTripData{id: "trip-2"})
	_, ok := cache.get(2, "trip-2")
	require.True(t, ok, "entry for the current dataset should be held")

	// The first request finally finishes and writes what it loaded from
	// generation 1.
	cache.put(1, "trip-1", blockTripData{id: "trip-1"})

	assert.EqualValues(t, 2, cache.generation.Load(), "generation must not move backward")
	_, ok = cache.get(2, "trip-2")
	assert.True(t, ok, "the late write must not clear the current dataset's entries")
	_, ok = cache.get(1, "trip-1")
	assert.False(t, ok, "data from the replaced dataset must not be stored")
}

func TestBlockTripDataCache_StopsInsertingAtCap(t *testing.T) {
	var cache blockTripDataCache
	for i := 0; i < maxBlockTripCacheEntries; i++ {
		id := fmt.Sprintf("trip-%d", i)
		cache.put(1, id, blockTripData{id: id})
	}
	require.EqualValues(t, maxBlockTripCacheEntries, cache.count.Load())

	cache.put(1, "one-too-many", blockTripData{id: "one-too-many"})
	_, ok := cache.get(1, "one-too-many")
	assert.False(t, ok, "insert past the cap should be dropped")
	assert.EqualValues(t, maxBlockTripCacheEntries, cache.count.Load(), "count must not grow past the cap")

	// Entries already held stay readable.
	_, ok = cache.get(1, "trip-0")
	assert.True(t, ok, "existing entries should still be served when full")
}

// TestBlockTripDataCache_ConcurrentAccess exists mainly to give CI's race
// detector something to inspect: the cache is shared by every in-flight request,
// so reads, writes and a reload landing mid-flight all overlap in production.
func TestBlockTripDataCache_ConcurrentAccess(t *testing.T) {
	var cache blockTripDataCache
	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("trip-%d", i%25)
				generation := uint64(i % 3)
				cache.put(generation, id, blockTripData{id: id})
				cache.get(generation, id)
				if i%50 == 0 {
					cache.reset(generation)
				}
			}
		}(worker)
	}
	wg.Wait()

	// Nothing to assert beyond surviving without a panic or a corrupted count.
	assert.GreaterOrEqual(t, cache.count.Load(), int64(0))
	assert.LessOrEqual(t, cache.count.Load(), int64(maxBlockTripCacheEntries))
}

func TestLoadBlockTripData_SecondCallServesFromCache(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	first := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, first, 1)
	require.NotEmpty(t, first[0].stopTimes)

	second := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, second, 1)
	require.NotEmpty(t, second[0].stopTimes)

	// A fresh query would allocate new rows, so sharing the backing array is what
	// shows the second call never reached the database.
	assert.Same(t, &first[0].stopTimes[0], &second[0].stopTimes[0],
		"second call should serve the cached stop times")
}

func TestLoadBlockTripData_ReturnsFreshSliceEachCall(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	tripID := findTripWithBlockAndShape(t, api)

	first := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, first, 1)

	second := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, second, 1)

	// computeScheduledBlockSnapshot sorts what it gets back in place, so each
	// caller needs its own outer slice or one request would reorder another's.
	assert.NotSame(t, &first[0], &second[0], "each call needs its own outer slice")

	// Overwriting one caller's slice must leave the cache intact.
	first[0] = blockTripData{id: "clobbered"}
	third := api.loadBlockTripData(ctx, []string{tripID})
	require.Len(t, third, 1)
	assert.Equal(t, tripID, third[0].id, "cached entry should survive a caller mutating its slice")
}

func TestLoadBlockTripData_DoesNotCacheFailedLoad(t *testing.T) {
	// apiWithClosedDB rather than createTestApi: the shared manager must stay open
	// for the rest of the package.
	api := apiWithClosedDB(t)
	ctx := context.Background()

	data := api.loadBlockTripData(ctx, []string{"any-trip"})
	assert.Empty(t, data, "a failed load returns nothing")
	assert.Zero(t, api.blockTripCache.count.Load(),
		"a failed load must not be cached, or one DB error would outlive the request")
}

func TestLoadBlockTripData_MixesCachedAndUncachedTrips(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	trips, err := api.GtfsManager.GetTrips(ctx, 50)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(trips), 2, "fixture needs at least two trips")

	withStopTimes := make([]string, 0, 2)
	for _, trip := range trips {
		if len(api.loadBlockTripData(ctx, []string{trip.ID})) == 1 {
			withStopTimes = append(withStopTimes, trip.ID)
		}
		if len(withStopTimes) == 2 {
			break
		}
	}
	require.Len(t, withStopTimes, 2, "fixture needs two trips with stop times")

	// Both are cached by the loop above. Asking for them together with a trip that
	// has no rows at all should return exactly the two that do, in order.
	got := api.loadBlockTripData(ctx, []string{withStopTimes[0], "trip-does-not-exist", withStopTimes[1]})
	require.Len(t, got, 2)
	assert.Equal(t, withStopTimes[0], got[0].id)
	assert.Equal(t, withStopTimes[1], got[1].id)
}
