package restapi

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"maglev.onebusaway.org/gtfsdb"
)

// requestCacheKey is the context key under which a request carries its memo.
type requestCacheKey struct{}

// requestCache memoizes GTFS lookups that repeat across the rows of a single
// response.
//
// Endpoints that build a trip status per row — vehicles-for-agency across every
// vehicle in an agency, for instance — resolve the same block and the same
// service calendar over and over. Without a memo the work scales with
// (rows × block size) rather than with the number of distinct blocks, and the
// service-ID lookup repeats once per row for a date that never changes within
// the request.
//
// The memo is deliberately request-scoped rather than a field on RestAPI: static
// GTFS data is reloaded in the background, and a longer-lived cache would need
// invalidation wired to that reload. It is mutex-guarded because nothing
// prevents a handler from building statuses concurrently.
//
// This complements the existing snapshotCache (see WithSnapshotCache), which
// memoizes whole block snapshots. These entries sit a level below that and are
// still reached on the schedule-deviation path, which resolves a block without
// going through a snapshot.
type requestCache struct {
	mu               sync.Mutex
	blockTripData    map[string][]blockTripData
	activeServiceIDs map[string][]string
	stopTimes        map[string][]gtfsdb.StopTime
}

// withRequestCache returns a context carrying a request-scoped memo. Handlers
// that build many trip statuses in one request should wrap their context with
// it; handlers that build a single trip status gain nothing and can leave it off.
func withRequestCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestCacheKey{}, &requestCache{
		blockTripData:    make(map[string][]blockTripData),
		activeServiceIDs: make(map[string][]string),
		stopTimes:        make(map[string][]gtfsdb.StopTime),
	})
}

// requestCacheFrom returns the request's memo, or nil when the handler did not
// opt in.
func requestCacheFrom(ctx context.Context) *requestCache {
	cache, _ := ctx.Value(requestCacheKey{}).(*requestCache)
	return cache
}

// blockTripDataCacheKeyFor joins trip IDs into a single map key. GTFS IDs cannot
// contain a NUL byte, so the separator cannot collide across different splits of
// the same concatenated text.
func blockTripDataCacheKeyFor(tripIDs []string) string {
	return strings.Join(tripIDs, "\x00")
}

func (c *requestCache) getBlockTripData(key string) ([]blockTripData, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	trips, ok := c.blockTripData[key]
	return trips, ok
}

func (c *requestCache) putBlockTripData(key string, trips []blockTripData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.blockTripData[key] = trips
}

func (c *requestCache) getActiveServiceIDs(date string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ids, ok := c.activeServiceIDs[date]
	return ids, ok
}

func (c *requestCache) putActiveServiceIDs(date string, ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activeServiceIDs[date] = ids
}

func (c *requestCache) getStopTimes(tripID string) ([]gtfsdb.StopTime, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stopTimes, ok := c.stopTimes[tripID]
	return stopTimes, ok
}

// prefetchStopTimes resolves the stop times of every trip in tripIDs with one
// query, seeding the request memo so the per-trip lookup inside BuildTripStatus
// becomes a map read instead of a query per response row.
//
// Trips that return no rows are recorded as empty entries, so a genuinely
// stop-time-less trip is not retried once per row.
//
// Shape points are deliberately NOT prefetched. GetShapePointsByTripIDs joins
// shapes to trips, so a shape shared by many trips is returned once per trip; a
// large agency's worth of vehicles would materialize hundreds of megabytes of
// duplicated geometry for the life of the request. Stop times are ~two orders of
// magnitude smaller per trip and carry no such duplication.
//
// A failed prefetch is logged and swallowed: an absent entry falls back to the
// per-trip query, so the response stays correct and is merely slower.
func (api *RestAPI) prefetchStopTimes(ctx context.Context, tripIDs []string) {
	cache := requestCacheFrom(ctx)
	if cache == nil || len(tripIDs) == 0 {
		return
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTripIDs(ctx, tripIDs)
	if err != nil {
		// Seeding after a failure would record "this trip has no stop times" for
		// every trip. An empty entry is a valid hit, so callers would skip the
		// per-trip fallback and emit a response missing the stop-relative fields
		// rather than a slower correct one.
		slog.Warn("prefetchStopTimes: GetStopTimesForTripIDs failed, falling back to per-trip lookups",
			slog.Int("trip_count", len(tripIDs)), slog.String("error", err.Error()))
		return
	}

	stopTimesByTrip := make(map[string][]gtfsdb.StopTime, len(tripIDs))
	for _, row := range rows {
		stopTimesByTrip[row.TripID] = append(stopTimesByTrip[row.TripID], row)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, tripID := range tripIDs {
		cache.stopTimes[tripID] = stopTimesByTrip[tripID]
	}
}

// stopTimesForTrip returns tripID's stop times, preferring a prefetched entry.
//
// A prefetched result is shared with every other caller in the request and must
// be treated as read-only. BuildTripStatus takes pointers into the backing array
// (&stopTimes[i]) and hands them to the stop-offset helpers, so a write through
// one of those pointers would corrupt the memo for the rest of the request.
func (api *RestAPI) stopTimesForTrip(ctx context.Context, tripID string) ([]gtfsdb.StopTime, error) {
	if cache := requestCacheFrom(ctx); cache != nil {
		if stopTimes, ok := cache.getStopTimes(tripID); ok {
			return stopTimes, nil
		}
	}
	return api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
}

// activeServiceIDsForDate returns the service IDs active on formattedDate
// (YYYYMMDD), memoized for the lifetime of the request. Errors are returned to
// the caller and never cached, so a transient DB failure does not stick for the
// rest of the request.
func (api *RestAPI) activeServiceIDsForDate(ctx context.Context, formattedDate string) ([]string, error) {
	cache := requestCacheFrom(ctx)
	if cache == nil {
		return api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	}

	if ids, ok := cache.getActiveServiceIDs(formattedDate); ok {
		return ids, nil
	}

	ids, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		return nil, err
	}
	cache.putActiveServiceIDs(formattedDate, ids)
	return ids, nil
}
