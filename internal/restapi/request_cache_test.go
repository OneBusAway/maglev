package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
)

// TestLoadBlockTripDataWithoutCache verifies the loader still works for handlers
// that do not opt into the request-scoped memo.
func TestLoadBlockTripDataWithoutCache(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	trips := api.loadBlockTripData(context.Background(), []string{trip.ID})
	require.Len(t, trips, 1)
	assert.Equal(t, trip.ID, trips[0].id)
}

// TestLoadBlockTripDataUsesCache verifies a second resolution of the same trip-ID
// set is served from the memo rather than the database. The memo is seeded with a
// sentinel so a hit is distinguishable from a fresh load.
func TestLoadBlockTripDataUsesCache(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	tripIDs := []string{trip.ID}
	ctx := withRequestCache(context.Background())

	cache := requestCacheFrom(ctx)
	require.NotNil(t, cache, "context must carry the memo")
	cache.putBlockTripData(blockTripDataCacheKeyFor(tripIDs), []blockTripData{{id: "sentinel"}})

	trips := api.loadBlockTripData(ctx, tripIDs)
	require.Len(t, trips, 1)
	assert.Equal(t, "sentinel", trips[0].id, "the memo must be consulted before the database")
}

// TestLoadBlockTripDataPopulatesCache verifies the first resolution stores its
// result, so later callers in the same request skip the load.
func TestLoadBlockTripDataPopulatesCache(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	tripIDs := []string{trip.ID}
	ctx := withRequestCache(context.Background())

	require.Len(t, api.loadBlockTripData(ctx, tripIDs), 1)

	cached, ok := requestCacheFrom(ctx).getBlockTripData(blockTripDataCacheKeyFor(tripIDs))
	require.True(t, ok, "the first resolution must populate the memo")
	require.Len(t, cached, 1)
	assert.Equal(t, trip.ID, cached[0].id)
}

// TestLoadBlockTripDataReturnsIndependentSlices verifies each caller gets its own
// backing array. loadShiftTrips sorts the result in place, which would otherwise
// reorder the cached entry underneath everyone else.
func TestLoadBlockTripDataReturnsIndependentSlices(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	tripIDs := []string{trip.ID}
	ctx := withRequestCache(context.Background())

	first := api.loadBlockTripData(ctx, tripIDs)
	require.Len(t, first, 1)
	first[0].id = "mutated by caller"

	second := api.loadBlockTripData(ctx, tripIDs)
	require.Len(t, second, 1)
	assert.Equal(t, trip.ID, second[0].id, "a caller's in-place edit must not leak into the memo")
}

// TestPrefetchStopTimesSeedsMemo verifies one batch query populates an entry per
// requested trip, including trips that genuinely have no stop times.
func TestPrefetchStopTimesSeedsMemo(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	const missingTripID = "trip-that-does-not-exist"
	ctx := withRequestCache(context.Background())

	api.prefetchStopTimes(ctx, []string{trip.ID, missingTripID})

	cache := requestCacheFrom(ctx)
	seeded, ok := cache.getStopTimes(trip.ID)
	require.True(t, ok, "a real trip must be seeded")
	assert.NotEmpty(t, seeded)

	empty, ok := cache.getStopTimes(missingTripID)
	require.True(t, ok, "a trip with no stop times must still be seeded, so it is not retried per row")
	assert.Empty(t, empty)
}

// TestPrefetchStopTimesWithoutCacheIsNoop verifies the prefetch is inert for
// handlers that did not opt in, rather than panicking.
func TestPrefetchStopTimesWithoutCacheIsNoop(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	api.prefetchStopTimes(context.Background(), []string{trip.ID})

	stopTimes, err := api.stopTimesForTrip(context.Background(), trip.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, stopTimes, "the lookup must still fall through to the query")
}

// TestStopTimesForTripPrefersMemo verifies the per-trip lookup reads a seeded
// entry instead of querying.
func TestStopTimesForTripPrefersMemo(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	ctx := withRequestCache(context.Background())
	requestCacheFrom(ctx).stopTimes[trip.ID] = []gtfsdb.StopTime{{TripID: "sentinel"}}

	stopTimes, err := api.stopTimesForTrip(ctx, trip.ID)
	require.NoError(t, err)
	require.Len(t, stopTimes, 1)
	assert.Equal(t, "sentinel", stopTimes[0].TripID, "the memo must be consulted before the database")
}

// BenchmarkArrivalsAndDeparturesWideWindow measures the arrivals handler over a
// window wide enough to return many rows, which is where the per-row trip status
// cost actually shows up.
//
// The existing BenchmarkArrivalsAndDeparturesForStop runs on the real clock against
// a fixture whose calendar expired, so it returns an empty list and never enters the
// per-arrival loop at all. This one holds the clock inside the fixture's service
// window and uses the same stop the handler tests use.
func BenchmarkArrivalsAndDeparturesWideWindow(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	endpoint := arrivalsAndDeparturesURL(arrivalsTestStopID, url.Values{
		"minutesBefore": {"120"},
		"minutesAfter":  {"480"},
	})

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, endpoint, nil)

	warmup := httptest.NewRecorder()
	mux.ServeHTTP(warmup, req)
	require.Equal(b, http.StatusOK, warmup.Code)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}
}

// cancelledWithin returns a cancelled child of ctx, so a query issued with it fails
// while the request memo carried on ctx stays reachable. This stands in for a
// transient database failure without disturbing the shared fixture.
func cancelledWithin(ctx context.Context) context.Context {
	failing, cancel := context.WithCancel(ctx)
	cancel()
	return failing
}

// TestLoadBlockTripDataDoesNotCacheFailedLoad verifies a failed load leaves the
// memo empty and a later call recovers. Caching the failure would pin one transient
// error to this block for every remaining row of the request.
func TestLoadBlockTripDataDoesNotCacheFailedLoad(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	tripIDs := []string{trip.ID}
	ctx := withRequestCache(context.Background())

	require.Empty(t, api.loadBlockTripData(cancelledWithin(ctx), tripIDs),
		"a failed load must not return trip data")

	_, cached := requestCacheFrom(ctx).getBlockTripData(blockTripDataCacheKeyFor(tripIDs))
	require.False(t, cached, "a failed load must not be memoized")

	recovered := api.loadBlockTripData(ctx, tripIDs)
	require.Len(t, recovered, 1, "a later call must retry rather than reuse the failure")
	assert.Equal(t, trip.ID, recovered[0].id)
}

// TestLoadBlockTripDataFromDBReportsIncompleteLoad verifies the completeness flag
// that gates memoization is false when the underlying queries fail.
func TestLoadBlockTripDataFromDBReportsIncompleteLoad(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)

	trips, complete := api.loadBlockTripDataFromDB(context.Background(), []string{trip.ID})
	require.Len(t, trips, 1)
	assert.True(t, complete, "a fully successful load must be reported as complete")

	trips, complete = api.loadBlockTripDataFromDB(cancelledWithin(context.Background()), []string{trip.ID})
	assert.Nil(t, trips)
	assert.False(t, complete, "a failed load must be reported as incomplete")
}

// TestPrefetchStopTimesDoesNotSeedOnFailure verifies a failed prefetch leaves the
// memo untouched, so per-trip lookups fall back to querying instead of reading an
// empty entry as though the trips genuinely had no stop times.
func TestPrefetchStopTimesDoesNotSeedOnFailure(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	trip := mustGetTrip(t, api)
	ctx := withRequestCache(context.Background())

	assert.Nil(t, api.prefetchStopTimes(cancelledWithin(ctx), []string{trip.ID}),
		"a failed prefetch must not report any rows")

	_, seeded := requestCacheFrom(ctx).getStopTimes(trip.ID)
	require.False(t, seeded, "a failed prefetch must leave the entry absent")

	stopTimes, err := api.stopTimesForTrip(ctx, trip.ID)
	require.NoError(t, err, "the per-trip lookup must still reach the database")
	assert.NotEmpty(t, stopTimes)
}

// TestActiveServiceIDsForDateDoesNotCacheError verifies a failed lookup is
// propagated and not memoized, so it does not stick for the rest of the request.
func TestActiveServiceIDsForDateDoesNotCacheError(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := withRequestCache(context.Background())
	const serviceDate = "20241104"

	_, err := api.activeServiceIDsForDate(cancelledWithin(ctx), serviceDate)
	require.Error(t, err, "the failure must be propagated to the caller")

	_, cached := requestCacheFrom(ctx).getActiveServiceIDs(serviceDate)
	require.False(t, cached, "a failed lookup must not be memoized")

	ids, err := api.activeServiceIDsForDate(ctx, serviceDate)
	require.NoError(t, err, "a later call must retry rather than reuse the failure")
	assert.NotEmpty(t, ids)
}

// TestActiveServiceIDsForDateWithoutCache verifies the lookup falls through to the
// query when the handler did not opt into the memo. The date is asserted to have
// service first, so an equality check against the direct query cannot pass by both
// sides being empty.
func TestActiveServiceIDsForDateWithoutCache(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	const serviceDate = "20241104" // Monday inside the RABA fixture's calendar

	expected, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDate)
	require.NoError(t, err)
	require.NotEmpty(t, expected,
		"fixture must have service on %s for this test to prove anything", serviceDate)

	ids, err := api.activeServiceIDsForDate(ctx, serviceDate)
	require.NoError(t, err)
	assert.Equal(t, expected, ids)
}

// TestActiveServiceIDsForDateDoesNotCacheEmptyResult verifies a date with no
// service is still answered correctly, and that the empty answer is memoized as a
// legitimate result rather than treated as a miss on every call.
func TestActiveServiceIDsForDateDoesNotConfuseEmptyWithMissing(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := withRequestCache(context.Background())
	const noServiceDate = "19700101" // far outside any RABA calendar window

	ids, err := api.activeServiceIDsForDate(ctx, noServiceDate)
	require.NoError(t, err)
	assert.Empty(t, ids)

	cached, ok := requestCacheFrom(ctx).getActiveServiceIDs(noServiceDate)
	assert.True(t, ok, "an empty-but-valid answer must be memoized, not re-queried per row")
	assert.Empty(t, cached)
}

// TestActiveServiceIDsForDateUsesCache verifies the date is resolved once per
// request. Every row of a list response asks for the same service date.
func TestActiveServiceIDsForDateUsesCache(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	const serviceDate = "20241104"
	ctx := withRequestCache(context.Background())

	first, err := api.activeServiceIDsForDate(ctx, serviceDate)
	require.NoError(t, err)

	cached, ok := requestCacheFrom(ctx).getActiveServiceIDs(serviceDate)
	require.True(t, ok, "the first lookup must populate the memo")
	assert.Equal(t, first, cached)

	// Overwrite the entry so a second call proves it reads through the memo.
	requestCacheFrom(ctx).putActiveServiceIDs(serviceDate, []string{"sentinel"})
	second, err := api.activeServiceIDsForDate(ctx, serviceDate)
	require.NoError(t, err)
	assert.Equal(t, []string{"sentinel"}, second, "the memo must be consulted before the database")
}
